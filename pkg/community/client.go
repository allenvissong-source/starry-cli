package community

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"starry-cli/pkg/transport"
)

type Config = transport.Config
type APIError = transport.APIError

// Client retains the community API used by CLI/MCP; transport is shared with
// the resource modules and contains no community business rules.
type Client struct {
	*transport.Client
	base, publicWeb string
	client          *http.Client
}

func NewClient(cfg Config) (*Client, error) {
	client, err := transport.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{Client: client, base: client.BackendURL(), publicWeb: client.PublicWebURL(), client: client.HTTPClient()}, nil
}

func newAPIError(status int, code string) *APIError {
	return transport.NewAPIError(status, code)
}
func retryableStatus(status int) bool { return status == 429 || status >= 500 }

type Principal struct {
	OwnerUserID          string   `json:"ownerUserId"`
	IntegrationID        string   `json:"integrationId"`
	CredentialID         string   `json:"credentialId"`
	CredentialKind       string   `json:"credentialKind"`
	Scopes               []string `json:"scopes"`
	GrantVersion         int64    `json:"grantVersion"`
	AllowedAIIdentityIDs []string `json:"allowedAiIdentityIds"`
}
type ExchangeResult struct {
	Token     string    `json:"token"`
	Principal Principal `json:"principal"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (b *Client) Exchange(ctx context.Context, key string) (ExchangeResult, error) {
	data, err := b.Request(ctx, "POST", "/api/v1/integration-token", key, nil)
	return decodeExchange(data, err)
}
func (b *Client) ExchangeForGateway(ctx context.Context, key, secret string) (ExchangeResult, error) {
	data, err := b.Request(ctx, "POST", "/internal/v1/integration-token/exchange", secret, map[string]string{"apiKey": key})
	return decodeExchange(data, err)
}
func decodeExchange(data json.RawMessage, err error) (ExchangeResult, error) {
	if err != nil {
		return ExchangeResult{}, err
	}
	var result ExchangeResult
	if json.Unmarshal(data, &result) != nil || result.Token == "" || result.Principal.OwnerUserID == "" {
		return ExchangeResult{}, newAPIError(503, "upstream_unavailable")
	}
	return result, nil
}
