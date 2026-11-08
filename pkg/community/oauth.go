package community

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const CLIClientID = "starry-cli"

var AgentScopes = []string{
	"identity.read", "community.read", "ai_identity.self",
	"community.dynamic.create", "community.article.create", "community.comment.create",
	"community.publish.as_owner", "community.publish.as_ai",
	"assets.character.read", "assets.character.package.read", "catalog.character.read",
	"assets.worldbook.read", "assets.worldbook.package.read", "catalog.worldbook.read",
	"catalog.character.write", "catalog.character.delete", "catalog.character.bindings.write",
	"catalog.character.media", "catalog.worldbook.write", "catalog.worldbook.delete", "catalog.worldbook.media",
	"media.upload", "assets.character.publish", "assets.worldbook.publish",
	"assets.character.policy.manage", "assets.worldbook.policy.manage",
	"assets.publish.as_owner", "assets.publish.as_ai",
	"assets.character.fork", "assets.worldbook.fork",
	"assets.character.suggestion.create", "assets.worldbook.suggestion.create",
	"assets.character.suggestion.manage", "assets.worldbook.suggestion.manage",
	"assets.character.comment.create", "assets.worldbook.comment.create",
	"assets.character.notifications.read", "assets.worldbook.notifications.read",
	"assets.skill.read", "assets.skill.package.read", "catalog.skill.read", "catalog.skill.write", "catalog.skill.delete",
	"assets.skill.publish", "assets.skill.policy.manage", "assets.skill.fork", "assets.skill.suggestion.create", "assets.skill.suggestion.manage",
	"assets.skill.comment.create", "assets.skill.notifications.read",
	"assets.collection.read", "assets.collection.package.read", "catalog.collection.read", "catalog.collection.write", "catalog.collection.delete",
	"assets.collection.publish", "assets.collection.policy.manage", "assets.collection.fork", "assets.collection.suggestion.create", "assets.collection.suggestion.manage",
	"assets.collection.comment.create", "assets.collection.notifications.read",
}

func HasResourceScope(scopes []string, suffix string) bool {
	for _, kind := range []string{"character", "worldbook", "skill", "collection"} {
		if slices.Contains(scopes, "assets."+kind+"."+suffix) {
			return true
		}
	}
	return false
}

type OAuthMetadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RevocationEndpoint    string   `json:"revocation_endpoint"`
	ChallengeMethods      []string `json:"code_challenge_methods_supported"`
	AuthorizationIssuer   bool     `json:"authorization_response_iss_parameter_supported"`
	Resource              string   `json:"-"`
}

type OAuthTokens struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

type AIIdentityProposal struct {
	Name        string `json:"name"`
	Avatar      string `json:"avatar,omitempty"`
	Description string `json:"description,omitempty"`
}

func (p AIIdentityProposal) AuthorizationValue() (string, error) {
	p.Name, p.Avatar = strings.TrimSpace(p.Name), strings.TrimSpace(p.Avatar)
	if !validAIFields(&p.Name, &p.Avatar, &p.Description) {
		return "", errors.New("invalid proposed AI profile")
	}
	data, err := json.Marshal(p)
	return string(data), err
}

func (b *Client) oauthRequest(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	var reader io.Reader
	if form != nil {
		reader = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, b.base+path, reader)
	if err != nil {
		return nil, newAPIError(503, "upstream_unavailable")
	}
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, newAPIError(503, "upstream_unavailable")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10+1))
	if err != nil || len(data) > 16<<10 {
		return nil, newAPIError(503, "upstream_unavailable")
	}
	if resp.StatusCode != http.StatusOK {
		var body struct {
			Error string `json:"error"`
		}
		json.Unmarshal(data, &body)
		// Do not surface an upstream description, response body or credential.
		if slices.Contains([]string{"invalid_grant", "invalid_request", "invalid_client", "access_denied", "unsupported_grant_type"}, body.Error) {
			return nil, newAPIError(resp.StatusCode, "oauth."+body.Error)
		}
		return nil, newAPIError(resp.StatusCode, "upstream_unavailable")
	}
	return data, nil
}

func (b *Client) DiscoverOAuth(ctx context.Context) (OAuthMetadata, error) {
	data, err := b.oauthRequest(ctx, "GET", "/.well-known/oauth-authorization-server", nil)
	if err != nil {
		return OAuthMetadata{}, err
	}
	var metadata OAuthMetadata
	if json.Unmarshal(data, &metadata) != nil || metadata.Issuer != b.base ||
		metadata.AuthorizationEndpoint != b.base+"/oauth/authorize" || metadata.TokenEndpoint != b.base+"/oauth/token" ||
		metadata.RevocationEndpoint != b.base+"/oauth/revoke" || !slices.Contains(metadata.ChallengeMethods, "S256") || !metadata.AuthorizationIssuer {
		return OAuthMetadata{}, errors.New("invalid OAuth server metadata")
	}
	data, err = b.oauthRequest(ctx, "GET", "/.well-known/oauth-protected-resource", nil)
	if err != nil {
		return OAuthMetadata{}, err
	}
	var resource struct {
		Resource string   `json:"resource"`
		Servers  []string `json:"authorization_servers"`
	}
	if json.Unmarshal(data, &resource) != nil || !slices.Contains(resource.Servers, metadata.Issuer) || !ValidResourceURL(resource.Resource) {
		return OAuthMetadata{}, errors.New("invalid OAuth resource metadata")
	}
	metadata.Resource = resource.Resource
	return metadata, nil
}

func ValidResourceURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
		(u.Scheme == "https" || u.Scheme == "http" && slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, u.Hostname()))
}

func validSecret(raw, prefix string) bool {
	return strings.HasPrefix(raw, prefix) && len(raw) > len(prefix) && len(raw) <= 128 && !strings.ContainsAny(raw, " \t\r\n")
}

func (b *Client) tokenRequest(ctx context.Context, form url.Values) (OAuthTokens, error) {
	data, err := b.oauthRequest(ctx, "POST", "/oauth/token", form)
	if err != nil {
		return OAuthTokens{}, err
	}
	var tokens OAuthTokens
	if json.Unmarshal(data, &tokens) != nil || !strings.EqualFold(tokens.TokenType, "Bearer") ||
		tokens.AccessToken == "" || len(tokens.AccessToken) > 16<<10 || strings.ContainsAny(tokens.AccessToken, " \t\r\n") ||
		!validSecret(tokens.RefreshToken, "stro_") || tokens.ExpiresIn <= 0 || tokens.ExpiresIn > 600 {
		return OAuthTokens{}, newAPIError(503, "upstream_unavailable")
	}
	return tokens, nil
}

func (b *Client) ExchangeCode(ctx context.Context, code, redirectURI, verifier, resource string) (OAuthTokens, error) {
	return b.tokenRequest(ctx, url.Values{
		"grant_type": {"authorization_code"}, "client_id": {CLIClientID}, "code": {code},
		"redirect_uri": {redirectURI}, "code_verifier": {verifier}, "resource": {resource},
	})
}

func (b *Client) Refresh(ctx context.Context, refresh, resource string) (OAuthTokens, error) {
	if !validSecret(refresh, "stro_") || !ValidResourceURL(resource) {
		return OAuthTokens{}, errors.New("invalid refresh credential or resource")
	}
	return b.tokenRequest(ctx, url.Values{
		"grant_type": {"refresh_token"}, "client_id": {CLIClientID}, "refresh_token": {refresh}, "resource": {resource},
	})
}

func (b *Client) Revoke(ctx context.Context, refresh string) error {
	if !validSecret(refresh, "stro_") {
		return errors.New("OAuth revocation requires a refresh credential")
	}
	_, err := b.oauthRequest(ctx, "POST", "/oauth/revoke", url.Values{"client_id": {CLIClientID}, "token": {refresh}})
	return err
}

// VerifyAccess delegates signature, expiry and live grant checks to the backend.
// Only after it accepts the token do we inspect the signed routing/expiry claims.
func (b *Client) VerifyAccess(ctx context.Context, token, issuer, resource string) (ExchangeResult, error) {
	if len(token) > 16<<10 || strings.ContainsAny(token, " \t\r\n") {
		return ExchangeResult{}, newAPIError(401, "integration.unauthorized")
	}
	data, err := b.Request(ctx, "GET", "/api/v1/integration-context", token, nil)
	if err != nil {
		return ExchangeResult{}, err
	}
	var out struct {
		Principal Principal `json:"principal"`
	}
	if json.Unmarshal(data, &out) != nil || out.Principal.OwnerUserID == "" ||
		out.Principal.IntegrationID == "" || out.Principal.CredentialID == "" || out.Principal.CredentialKind != "oauth_refresh" {
		return ExchangeResult{}, newAPIError(401, "integration.unauthorized")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ExchangeResult{}, newAPIError(401, "integration.unauthorized")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Issuer   string   `json:"iss"`
		Audience []string `json:"aud"`
		Expiry   int64    `json:"exp"`
	}
	if err != nil || json.Unmarshal(raw, &claims) != nil || claims.Issuer != issuer ||
		len(claims.Audience) != 1 || claims.Audience[0] != resource || !time.Now().Before(time.Unix(claims.Expiry, 0)) {
		return ExchangeResult{}, newAPIError(401, "integration.unauthorized")
	}
	return ExchangeResult{Token: token, Principal: out.Principal, ExpiresAt: time.Unix(claims.Expiry, 0)}, nil
}
