package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allenvissong-source/starry-cli/pkg/community"
)

// Only the backend's disposable Gin fixture enables this test. The browser
// consent uses a fixture human session, not an external identity provider.
func TestLiveBackendOAuthOnboarding(t *testing.T) {
	base, owner := os.Getenv("STARRY_E2E_BACKEND"), os.Getenv("STARRY_E2E_OWNER_TOKEN")
	if base == "" || owner == "" {
		t.Skip("disposable OAuth backend not configured")
	}
	t.Setenv("STARRY_BACKEND_URL", base)
	t.Setenv("STARRY_API_KEY", "")
	t.Setenv("STARRY_CLI_CONFIG", "")
	t.Setenv("STARRY_PUBLIC_WEB_URL", "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := community.NewClient(community.Config{BackendURL: base})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	var output, diagnostics bytes.Buffer
	browser := func(ctx context.Context, target string) error {
		httpClient := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		location, err := url.Parse(resp.Header.Get("Location"))
		if err != nil || resp.StatusCode != 302 || location.Path != "/connect/authorize" {
			return errors.New("authorization did not reach consent")
		}
		consentPath := "/api/v1/integration-authorization-requests/" + location.Query().Get("request")
		if _, err := client.Request(ctx, "GET", consentPath, "", nil); err == nil {
			return errors.New("consent accepted an anonymous caller")
		}
		view, err := client.Request(ctx, "GET", consentPath, owner, nil)
		var requested struct {
			ClientID   string                        `json:"clientId"`
			Scopes     []string                      `json:"scopes"`
			ProposedAI *community.AIIdentityProposal `json:"proposedAiIdentity"`
		}
		if err != nil || json.Unmarshal(view, &requested) != nil || requested.ClientID != community.CLIClientID ||
			requested.ProposedAI == nil || requested.ProposedAI.Name != "OAuth CLI AI" {
			return errors.New("cannot inspect consent")
		}
		data, err := client.Request(ctx, "POST", consentPath+"/approve", owner,
			map[string]any{"consent": true, "scopes": requested.Scopes, "allowedAiIdentityIds": []string{}, "expiresInDays": 1, "proposedAiIdentity": requested.ProposedAI})
		var approved struct {
			RedirectURI string `json:"redirectUri"`
		}
		if err != nil || json.Unmarshal(data, &approved) != nil || approved.RedirectURI == "" {
			return errors.New("cannot approve consent")
		}
		req, _ = http.NewRequestWithContext(ctx, "GET", approved.RedirectURI, nil)
		resp, err = httpClient.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return errors.New("loopback rejected consent callback")
		}
		return nil
	}
	if exit := runLoginWithBrowser(ctx, path, 30*time.Second, &output, &diagnostics, browser,
		&community.AIIdentityProposal{Name: "OAuth CLI AI", Description: "original"}); exit != 0 {
		t.Fatalf("login failed: %s", diagnostics.String())
	}
	initial, err := readConfig(path)
	info, statErr := os.Stat(path)
	if err != nil || statErr != nil || info.Mode().Perm() != 0600 || !strings.HasPrefix(initial.APIKey, "stro_") {
		t.Fatal("login did not save private OAuth credentials")
	}
	if strings.Contains(output.String()+diagnostics.String(), initial.APIKey) || strings.Contains(output.String()+diagnostics.String(), initial.AccessToken) {
		t.Fatal("login leaked credentials")
	}
	call := func(command, action, input string) string {
		args := []string{command}
		if action != "" {
			args = append(args, action)
		}
		args = append(args, "--config", path)
		if input != "" {
			args = append(args, "--input", "-")
		}
		return liveCall(t, args, input)
	}
	call("whoami", "", "")
	listed := call("identity", "list", "")
	var identity struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(listed), &identity) != nil || len(identity.Data) != 1 || identity.Data[0].ID == "" {
		t.Fatal("consent did not create and allow the reviewed identity")
	}
	identityID := identity.Data[0].ID
	current, err := readConfig(path)
	if err != nil || current.RefreshUncertain {
		t.Fatal("consent credentials were not persisted")
	}
	if _, err := client.CreateAI(ctx, current.AccessToken, community.AIIdentityCreateInput{RequestKey: "unreviewed", Name: "Unreviewed"}); err == nil {
		t.Fatal("OAuth creation bypassed consent")
	}
	call("identity", "update", `{"id":"`+identityID+`","name":"OAuth CLI Updated"}`)
	input := `{"requestKey":"oauth-cli-post","publishAs":"ai","aiIdentityId":"` + identityID + `","nodeId":"1","content":"OAuth CLI post"}`
	first := call("create-dynamic", "", input)
	if !strings.Contains(first, "pending_review") || call("create-dynamic", "", input) != first {
		t.Fatal("AI publishing lost pending-review or retry semantics")
	}
	// A manual key on the same connection must survive OAuth family revocation.
	auth, err := client.VerifyAccess(ctx, current.AccessToken, current.OAuthIssuer, current.Resource)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := client.Request(ctx, "POST", "/api/v1/integrations/"+auth.Principal.IntegrationID+"/keys", owner, map[string]int{"expiresInDays": 1})
	var manual struct {
		APIKey string `json:"apiKey"`
	}
	if err != nil || json.Unmarshal(raw, &manual) != nil || manual.APIKey == "" {
		t.Fatal("cannot issue same-connection manual key")
	}
	call("logout", "", "")
	if _, err := client.VerifyAccess(ctx, current.AccessToken, current.OAuthIssuer, current.Resource); err == nil {
		t.Fatal("logout did not revoke access immediately")
	}
	if _, err := client.Refresh(ctx, current.APIKey, current.Resource); err == nil {
		t.Fatal("revoked refresh credential accepted")
	}
	loggedOut, err := readConfig(path)
	if err != nil || loggedOut.APIKey != "" || loggedOut.AccessToken != "" {
		t.Fatal("logout did not clear credentials")
	}
	if _, err := client.Exchange(ctx, manual.APIKey); err != nil {
		t.Fatal("OAuth logout revoked the manual key", err)
	}
}
