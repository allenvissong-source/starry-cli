package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"starry-cli/pkg/community"
)

func randomOAuthValue() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", errors.New("cannot generate OAuth challenge")
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func openBrowser(ctx context.Context, target string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command, args = "open", []string{target}
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", target}
	default:
		command, args = "xdg-open", []string{target}
	}
	openCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(openCtx, command, args...).Run()
}

type loginResult struct {
	code string
	err  error
}

func callbackHandler(state, issuer, host string, results chan<- loginResult) http.Handler {
	var completed atomic.Bool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.URL.Path != "/callback" || r.Method != "GET" || r.Host != host || len(r.URL.RawQuery) > 4096 {
			http.Error(w, "Invalid callback", 400)
			return
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(q["state"]) != 1 || len(q["iss"]) != 1 ||
			subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 || q.Get("iss") != issuer {
			http.Error(w, "Invalid authorization state or issuer", 400)
			return
		}
		result := loginResult{code: q.Get("code")}
		if len(q["error"]) > 0 {
			if len(q["error"]) != 1 || len(q["code"]) != 0 {
				http.Error(w, "Invalid authorization response", 400)
				return
			}
			result = loginResult{err: errors.New("authorization denied; run login to try again")}
		} else if len(q["code"]) != 1 || result.code == "" || len(result.code) > 1024 {
			http.Error(w, "Missing authorization code", 400)
			return
		}
		if !completed.CompareAndSwap(false, true) {
			http.Error(w, "Authorization already received", 409)
			return
		}
		results <- result
		fmt.Fprint(w, "Authorization received. Return to Starry CLI to finish.")
	})
}

func login(ctx context.Context, client *community.Client, browser func(context.Context, string) error, stderr io.Writer, proposal *community.AIIdentityProposal) (community.OAuthMetadata, community.OAuthTokens, error) {
	metadata, err := client.DiscoverOAuth(ctx)
	if err != nil {
		return metadata, community.OAuthTokens{}, err
	}
	state, err := randomOAuthValue()
	if err != nil {
		return metadata, community.OAuthTokens{}, err
	}
	verifier, err := randomOAuthValue()
	if err != nil {
		return metadata, community.OAuthTokens{}, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return metadata, community.OAuthTokens{}, errors.New("cannot open loopback callback")
	}
	host := listener.Addr().String()
	redirect := "http://" + host + "/callback"
	results := make(chan loginResult, 1)
	server := &http.Server{Handler: callbackHandler(state, metadata.Issuer, host, results), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, MaxHeaderBytes: 8 << 10}
	defer server.Close()
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"response_type": {"code"}, "client_id": {community.CLIClientID}, "redirect_uri": {redirect},
		"scope": {strings.Join(community.AgentScopes, " ")}, "state": {state}, "resource": {metadata.Resource},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"},
	}
	if proposal != nil {
		value, err := proposal.AuthorizationValue()
		if err != nil {
			return metadata, community.OAuthTokens{}, err
		}
		query.Set("ai_identity", value)
	}
	target := metadata.AuthorizationEndpoint + "?" + query.Encode()
	if browser(ctx, target) != nil {
		fmt.Fprintf(stderr, "Open this URL to authorize Starry CLI:\n%s\n", target)
	} else {
		fmt.Fprintln(stderr, "Complete authorization in the browser.")
	}
	select {
	case <-ctx.Done():
		return metadata, community.OAuthTokens{}, errors.New("authorization timed out or cancelled; run login again")
	case <-serveErrors:
		return metadata, community.OAuthTokens{}, errors.New("loopback callback stopped")
	case result := <-results:
		if result.err != nil {
			return metadata, community.OAuthTokens{}, result.err
		}
		tokens, err := client.ExchangeCode(ctx, result.code, redirect, verifier, metadata.Resource)
		return metadata, tokens, err
	}
}

func runLogin(ctx context.Context, path string, timeout time.Duration, stdout, stderr io.Writer, proposal *community.AIIdentityProposal) int {
	return runLoginWithBrowser(ctx, path, timeout, stdout, stderr, openBrowser, proposal)
}

func runLoginWithBrowser(ctx context.Context, path string, timeout time.Duration, stdout, stderr io.Writer, browser func(context.Context, string) error, proposal *community.AIIdentityProposal) int {
	if os.Getenv("STARRY_API_KEY") != "" {
		return report(stderr, errors.New("unset STARRY_API_KEY before browser login"))
	}
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	if timeout > 10*time.Minute {
		return report(stderr, errors.New("login timeout must not exceed 10 minutes"))
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	lock, err := lockConfig(ctx, path)
	if err != nil {
		return report(stderr, err)
	}
	defer closeConfigLock(lock)
	var cfg config
	if _, err := os.Lstat(path); err == nil {
		cfg, err = readConfig(path)
		if err != nil {
			return report(stderr, err)
		}
	} else if !os.IsNotExist(err) {
		return report(stderr, errors.New("cannot read credential config"))
	}
	if value := os.Getenv("STARRY_BACKEND_URL"); value != "" {
		cfg.BackendURL = value
	}
	if value := os.Getenv("STARRY_PUBLIC_WEB_URL"); value != "" {
		cfg.PublicWebURL = value
	}
	client, err := community.NewClient(community.Config{BackendURL: cfg.BackendURL, PublicWebURL: cfg.PublicWebURL})
	if err != nil {
		return report(stderr, err)
	}
	metadata, tokens, err := login(ctx, client, browser, stderr, proposal)
	if err != nil {
		return report(stderr, err)
	}
	cfg.BackendURL, cfg.OAuthIssuer, cfg.Resource = client.BackendURL(), metadata.Issuer, metadata.Resource
	storeTokens(&cfg, tokens)
	if err := saveConfig(path, cfg); err != nil {
		return report(stderr, err)
	}
	auth, err := client.VerifyAccess(ctx, tokens.AccessToken, metadata.Issuer, metadata.Resource)
	if err != nil {
		return report(stderr, err)
	}
	if err := json.NewEncoder(stdout).Encode(map[string]any{"data": auth.Principal, "loggedIn": true}); err != nil {
		return report(stderr, errors.New("cannot write output"))
	}
	return 0
}
