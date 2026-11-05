package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"starry-cli/pkg/community"
)

func testOAuthJWT(issuer, resource string, expiry time.Time) string {
	raw, _ := json.Marshal(map[string]any{"iss": issuer, "aud": []string{resource}, "exp": expiry.Unix()})
	return "header." + base64.RawURLEncoding.EncodeToString(raw) + ".signature"
}

func privateConfig(t *testing.T, cfg config) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := saveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoopbackLoginPKCEFallbackAndTimeout(t *testing.T) {
	var issuer, challenge string
	resource := "https://mcp.example.com/mcp"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]any{
				"issuer": issuer, "authorization_endpoint": issuer + "/oauth/authorize", "token_endpoint": issuer + "/oauth/token",
				"revocation_endpoint": issuer + "/oauth/revoke", "code_challenge_methods_supported": []string{"S256"},
				"authorization_response_iss_parameter_supported": true,
			})
		case "/.well-known/oauth-protected-resource":
			json.NewEncoder(w).Encode(map[string]any{"resource": resource, "authorization_servers": []string{issuer}})
		case "/oauth/token":
			r.ParseForm()
			hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(hash[:]) != challenge || r.Form.Get("code") != "one-time-code" ||
				r.Form.Get("client_id") != community.CLIClientID || r.Form.Get("resource") != resource ||
				!strings.HasPrefix(r.Form.Get("redirect_uri"), "http://127.0.0.1:") {
				t.Error("PKCE code exchange binding mismatch")
			}
			json.NewEncoder(w).Encode(community.OAuthTokens{TokenType: "Bearer", AccessToken: "access-secret", RefreshToken: "stro_new-secret", ExpiresIn: 600})
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	issuer = server.URL
	client, _ := community.NewClient(community.Config{BackendURL: issuer})
	var output bytes.Buffer
	metadata, tokens, err := login(context.Background(), client, func(_ context.Context, target string) error {
		u, _ := url.Parse(target)
		q := u.Query()
		challenge = q.Get("code_challenge")
		if q.Get("state") == "" || q.Get("code_challenge_method") != "S256" || q.Get("scope") != strings.Join(community.AgentScopes, " ") {
			t.Error("missing authorization binding")
		}
		var proposal community.AIIdentityProposal
		if json.Unmarshal([]byte(q.Get("ai_identity")), &proposal) != nil || proposal.Name != "Proposed AI" || proposal.Description != "Reviewed before consent" {
			t.Error("missing proposed AI profile")
		}
		callback := q.Get("redirect_uri")
		// A forged callback must not consume the valid pending authorization.
		for _, values := range []url.Values{
			{"state": {"forged"}, "iss": {issuer}, "code": {"forged"}},
			{"state": {q.Get("state")}, "iss": {"https://wrong.example"}, "code": {"forged"}},
			{"state": {q.Get("state"), q.Get("state")}, "iss": {issuer}, "code": {"forged"}},
			{"state": {q.Get("state")}, "iss": {issuer}, "code": {"one-time-code"}},
		} {
			resp, err := http.Get(callback + "?" + values.Encode())
			if err != nil {
				t.Error(err)
				continue
			}
			resp.Body.Close()
			want := 400
			if values.Get("code") == "one-time-code" {
				want = 200
			}
			if resp.StatusCode != want {
				t.Error(resp.StatusCode, want)
			}
		}
		return errors.New("no desktop browser")
	}, &output, &community.AIIdentityProposal{Name: "Proposed AI", Description: "Reviewed before consent"})
	if err != nil || metadata.Resource != resource || tokens.RefreshToken != "stro_new-secret" ||
		!strings.Contains(output.String(), issuer+"/oauth/authorize?") || strings.Contains(output.String(), "access-secret") {
		t.Fatal(metadata, err, output.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _, err = login(ctx, client, func(context.Context, string) error { return nil }, &output, nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatal("login did not stop on timeout", err)
	}
}

func TestCallbackDenialIsSingleUse(t *testing.T) {
	results := make(chan loginResult, 1)
	handler := callbackHandler("state", "https://issuer.example", "127.0.0.1:1234", results)
	target := "http://127.0.0.1:1234/callback?state=state&iss=https%3A%2F%2Fissuer.example&error=access_denied"
	for _, status := range []int{200, 409} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", target, nil))
		if response.Code != status {
			t.Fatal(response.Code)
		}
	}
	if result := <-results; result.err == nil || result.code != "" {
		t.Fatal("denial accepted as code")
	}
}

func TestConcurrentOAuthRefreshPersistReuseGrantChangeAndLogout(t *testing.T) {
	t.Setenv("STARRY_API_KEY", "")
	t.Setenv("STARRY_BACKEND_URL", "")
	t.Setenv("STARRY_PUBLIC_WEB_URL", "")
	var mu sync.Mutex
	refreshes, version := 0, 1
	currentRefresh, currentAccess := "stro_initial", ""
	revoked := false
	var issuer string
	resource := "https://mcp.example.com/mcp"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/oauth/token":
			r.ParseForm()
			if r.Form.Get("refresh_token") != currentRefresh || revoked {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			refreshes++
			currentRefresh = fmt.Sprintf("stro_rotation-%d", refreshes)
			currentAccess = testOAuthJWT(issuer, resource, time.Now().Add(10*time.Minute)) + fmt.Sprint(version)
			json.NewEncoder(w).Encode(community.OAuthTokens{TokenType: "Bearer", AccessToken: currentAccess, RefreshToken: currentRefresh, ExpiresIn: 600})
		case "/api/v1/integration-context":
			if r.Header.Get("Authorization") != "Bearer "+currentAccess || revoked {
				w.WriteHeader(401)
				w.Write([]byte(`{"success":false,"error":{"code":"integration.unauthorized"}}`))
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"principal": community.Principal{
				OwnerUserID: "owner", IntegrationID: "connection", CredentialID: "family", CredentialKind: "oauth_refresh", GrantVersion: int64(version),
			}}})
		case "/oauth/revoke":
			r.ParseForm()
			if r.Form.Get("token") != currentRefresh {
				t.Error("logout revoked stale refresh")
			}
			revoked = true
			w.Write([]byte(`{}`))
		default:
			t.Error("OAuth used manual exchange", r.URL.Path)
		}
	}))
	defer server.Close()
	issuer = server.URL
	initial := config{BackendURL: issuer, OAuthIssuer: issuer, Resource: resource, APIKey: currentRefresh}
	path := privateConfig(t, initial)
	client, _ := community.NewClient(community.Config{BackendURL: issuer})
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			p := &credentialProvider{client: client, path: path, cfg: initial}
			if _, err := p.access(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	mu.Lock()
	if refreshes != 1 {
		t.Error("concurrent processes rotated the same intent", refreshes)
	}
	version++
	currentAccess = "grant-no-longer-valid"
	mu.Unlock()
	p := &credentialProvider{client: client, path: path, cfg: initial}
	auth, err := p.access(context.Background())
	if err != nil || auth.Principal.GrantVersion != 2 {
		t.Fatal("grant change did not refresh", err)
	}
	cfg, err := readConfig(path)
	if err != nil || cfg.APIKey != "stro_rotation-2" || cfg.AccessToken == "" {
		t.Fatal("rotation was not persisted", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("credential file not private")
	}
	if err := p.logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg, _ = readConfig(path)
	if cfg.APIKey != "" || cfg.AccessToken != "" {
		t.Fatal("logout retained credentials")
	}
	if _, err := p.access(context.Background()); err == nil {
		t.Fatal("logout still authenticated")
	}
}

func TestOAuthLockCancellationAndEnvRefreshRejected(t *testing.T) {
	path := privateConfig(t, config{BackendURL: "https://api.example.com", APIKey: "stro_secret"})
	lock, err := lockConfig(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeConfigLock(lock)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if second, err := lockConfig(ctx, path); err == nil {
		closeConfigLock(second)
		t.Fatal("concurrent refresh lock acquired")
	}
	t.Setenv("STARRY_API_KEY", "stro_environment-secret")
	if _, err := loadConfig(path); err == nil {
		t.Fatal("non-persistable refresh accepted")
	}
}

func TestLostRefreshResponseBlocksRetryAcrossProcesses(t *testing.T) {
	refreshes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Error("unexpected access after uncertain refresh")
		}
		refreshes++
		w.WriteHeader(503)
		w.Write([]byte(`{"error":"temporarily_unavailable"}`))
	}))
	defer server.Close()
	cfg := config{BackendURL: server.URL, OAuthIssuer: server.URL, Resource: "https://mcp.example.com/mcp", APIKey: "stro_initial"}
	path := privateConfig(t, cfg)
	client, _ := community.NewClient(community.Config{BackendURL: server.URL})
	for i := 0; i < 2; i++ {
		p := &credentialProvider{client: client, path: path, cfg: cfg}
		if _, err := p.access(context.Background()); err == nil {
			t.Fatal("uncertain refresh was accepted")
		}
	}
	stored, _ := readConfig(path)
	if !stored.RefreshUncertain || refreshes != 1 {
		t.Fatal("spent refresh retried after restart", refreshes)
	}
}
