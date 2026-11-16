package community

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOAuthDiscoveryBindingAndRawForms(t *testing.T) {
	resource := "https://mcp.example/mcp"
	var server *httptest.Server
	var forms []map[string]string
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("OAuth public endpoint received bearer credentials")
		}
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]any{
				"issuer": server.URL, "authorization_endpoint": server.URL + "/oauth/authorize",
				"token_endpoint": server.URL + "/oauth/token", "revocation_endpoint": server.URL + "/oauth/revoke",
				"code_challenge_methods_supported": []string{"S256"}, "authorization_response_iss_parameter_supported": true,
			})
		case "/.well-known/oauth-protected-resource":
			json.NewEncoder(w).Encode(map[string]any{"resource": resource, "authorization_servers": []string{server.URL}})
		case "/oauth/token", "/oauth/revoke":
			if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.URL.RawQuery != "" {
				t.Error("not a body-only OAuth form")
			}
			r.ParseForm()
			form := map[string]string{}
			for key := range r.PostForm {
				form[key] = r.PostForm.Get(key)
			}
			forms = append(forms, form)
			if r.URL.Path == "/oauth/token" {
				w.Write([]byte(`{"access_token":"delegate","token_type":"Bearer","expires_in":600,"refresh_token":"stro_next.secret","scope":"identity.read"}`))
			}
		}
	}))
	defer server.Close()
	client, _ := NewClient(Config{BackendURL: server.URL})
	metadata, err := client.DiscoverOAuth(context.Background())
	if err != nil || metadata.Resource != resource {
		t.Fatal(metadata, err)
	}
	tokens, err := client.ExchangeCode(context.Background(), "code", "http://127.0.0.1:1234/callback", "verifier", resource)
	if err != nil || tokens.RefreshToken != "stro_next.secret" {
		t.Fatal(err)
	}
	if _, err := client.Refresh(context.Background(), tokens.RefreshToken, resource); err != nil {
		t.Fatal(err)
	}
	if err := client.Revoke(context.Background(), tokens.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if len(forms) != 3 || forms[0]["client_id"] != CLIClientID || forms[0]["resource"] != resource ||
		forms[0]["code_verifier"] != "verifier" || forms[1]["refresh_token"] != tokens.RefreshToken || forms[2]["token"] != tokens.RefreshToken {
		t.Fatal("form binding lost")
	}
	if _, err := client.Refresh(context.Background(), "stai_manual", resource); err == nil {
		t.Fatal("manual key refreshed")
	}
	if len(forms) != 3 {
		t.Fatal("manual key sent to OAuth")
	}
}

func TestOAuthFailuresNeverExposeSecretsOrFollowRedirects(t *testing.T) {
	for _, status := range []int{400, 302, 200} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "https://secret.example/credential")
			w.WriteHeader(status)
			w.Write([]byte(`{"error":"invalid_grant","error_description":"stro_private.secret","access_token":"stro_private.secret"}`))
		}))
		client, _ := NewClient(Config{BackendURL: server.URL})
		_, err := client.Refresh(context.Background(), "stro_original.secret", "https://mcp.example/mcp")
		server.Close()
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe or accepted OAuth failure", err)
		}
	}
}

func TestVerifyOAuthAccessUsesBackendThenChecksExactAudienceAndExpiry(t *testing.T) {
	accepted := true
	kind := "oauth_refresh"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/integration-context" {
			t.Error("wrong verifier")
		}
		if !accepted {
			w.WriteHeader(401)
			w.Write([]byte(`{"success":false,"error":{"code":"integration.unauthorized"}}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"principal": map[string]any{
			"ownerUserId": "1000", "integrationId": "connection", "credentialId": "family", "credentialKind": kind, "scopes": []string{"community.read"},
		}}})
	}))
	defer server.Close()
	client, _ := NewClient(Config{BackendURL: server.URL})
	token := func(audience string, expiry time.Time) string {
		payload := fmt.Sprintf(`{"iss":"%s","aud":["%s"],"exp":%d}`, server.URL, audience, expiry.Unix())
		return "header." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"
	}
	resource := "https://mcp.example/mcp"
	raw := token(resource, time.Now().Add(time.Minute))
	out, err := client.VerifyAccess(context.Background(), raw, server.URL, resource)
	if err != nil || out.Principal.CredentialID != "family" || out.ExpiresAt.IsZero() {
		t.Fatal(out, err)
	}
	for _, bad := range []string{token(resource+"/other", time.Now().Add(time.Minute)), token(resource, time.Now().Add(-time.Minute))} {
		if _, err := client.VerifyAccess(context.Background(), bad, server.URL, resource); err == nil {
			t.Fatal("invalid routing or expiry accepted")
		}
	}
	kind = "manual"
	if _, err := client.VerifyAccess(context.Background(), raw, server.URL, resource); err == nil {
		t.Fatal("manual JWT accepted as OAuth")
	}
	accepted = false
	if _, err := client.VerifyAccess(context.Background(), raw, server.URL, resource); err == nil {
		t.Fatal("backend rejection ignored")
	}
	if calls != 5 {
		t.Fatal(calls)
	}
}

func TestAIIdentityInputPreservesPartialUpdateAndCreationIntent(t *testing.T) {
	var bodies []map[string]any
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/integration/ai-identities" && r.URL.Path != "/api/v1/integration/ai-identities/ai-1" {
			t.Error("wrong identity path")
		}
		body := map[string]any{}
		if r.Method != "GET" {
			json.NewDecoder(r.Body).Decode(&body)
		}
		bodies = append(bodies, body)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.Write([]byte(`{"success":true,"data":{"id":"ai-1","name":"AI"}}`))
	}))
	defer server.Close()
	client, _ := NewClient(Config{BackendURL: server.URL})
	empty := ""
	if _, err := client.UpdateAI(context.Background(), "delegate", AIIdentityUpdateInput{ID: "ai-1", Description: &empty}); err != nil {
		t.Fatal(err)
	}
	input := AIIdentityCreateInput{RequestKey: "stable-intent", Name: " AI ", Description: "profile"}
	for i := 0; i < 2; i++ {
		out, err := client.CreateAI(context.Background(), "delegate", input)
		if err != nil || out["refreshRequired"] != true {
			t.Fatal(out, err)
		}
	}
	if len(bodies[0]) != 1 || bodies[0]["description"] != "" ||
		bodies[1]["name"] != "AI" || keys[1] != keys[2] || keys[1] != input.RequestKey {
		t.Fatal("partial update or retry intent changed")
	}
	for _, field := range []string{"id", "ownerUserId", "createdByIntegrationId", "disabled", "requestKey"} {
		if _, ok := bodies[1][field]; ok {
			t.Fatal("untrusted identity field", field)
		}
	}
	if _, err := client.Call(context.Background(), "delegate", "ai_identity_update", json.RawMessage(`{"id":"ai-1","disabled":true}`)); err == nil {
		t.Fatal("privileged update accepted")
	}
}
