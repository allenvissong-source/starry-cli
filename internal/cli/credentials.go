package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"starry-cli/pkg/community"
)

func validCredential(value string) bool {
	return (strings.HasPrefix(value, "stai_") || strings.HasPrefix(value, "stro_")) &&
		len(value) > 5 && len(value) <= 128 && !strings.ContainsAny(value, " \t\r\n")
}

func resolveConfigPath(path string, create bool) (string, error) {
	if path != "" {
		return filepath.Abs(path)
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", errors.New("cannot locate user config directory")
	}
	dir := filepath.Join(root, "starry-cli")
	path = filepath.Join(dir, "config.json")
	if create {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", errors.New("cannot create private config directory")
		}
	} else if _, err := os.Lstat(path); os.IsNotExist(err) {
		return "", nil
	}
	return path, nil
}

func lockConfig(ctx context.Context, path string) (*os.File, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := privateStatePath(path); err != nil {
		return nil, errors.New("config requires a private directory and regular 0600 config/lock files")
	}
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("cannot open credential lock")
	}
	for {
		if lockState(file) == nil {
			return file, nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			file.Close()
			return nil, errors.New("credential config busy or operation cancelled")
		case <-timer.C:
		}
	}
}

func closeConfigLock(file *os.File) {
	unlockState(file)
	file.Close()
}

func saveConfig(path string, cfg config) error {
	raw, err := json.Marshal(cfg)
	if err != nil || len(raw) > 16<<10 {
		return errors.New("credential config exceeds limit")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".starry-credentials-*")
	if err != nil {
		return errors.New("cannot create private credential file")
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.New("cannot persist credentials")
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return errors.New("cannot replace credentials")
	}
	if runtime.GOOS != "windows" {
		dir, err := os.Open(filepath.Dir(path))
		if err != nil {
			return errors.New("cannot sync credential directory")
		}
		err = dir.Sync()
		dir.Close()
		if err != nil {
			return errors.New("cannot sync credential directory")
		}
	}
	return nil
}

func storeTokens(cfg *config, tokens community.OAuthTokens) {
	cfg.APIKey, cfg.AccessToken = tokens.RefreshToken, tokens.AccessToken
	cfg.AccessExpiry = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	cfg.RefreshUncertain = false
}

type credentialProvider struct {
	mu     sync.Mutex
	client *community.Client
	path   string
	cfg    config
}

func (p *credentialProvider) access(ctx context.Context) (community.ExchangeResult, error) {
	return p.authenticate(ctx, false)
}

func (p *credentialProvider) authenticate(ctx context.Context, force bool) (community.ExchangeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.HasPrefix(p.cfg.APIKey, "stai_") {
		return p.client.Exchange(ctx, p.cfg.APIKey)
	}
	lock, err := lockConfig(ctx, p.path)
	if err != nil {
		return community.ExchangeResult{}, err
	}
	defer closeConfigLock(lock)
	// Another process may already have rotated the refresh token.
	cfg, err := readConfig(p.path)
	if err != nil || !validCredential(cfg.APIKey) || !strings.HasPrefix(cfg.APIKey, "stro_") ||
		strings.TrimRight(cfg.BackendURL, "/") != p.client.BackendURL() ||
		cfg.OAuthIssuer != p.client.BackendURL() || !community.ValidResourceURL(cfg.Resource) {
		return community.ExchangeResult{}, errors.New("OAuth config changed or invalid; run login")
	}
	if cfg.RefreshUncertain {
		return community.ExchangeResult{}, errors.New("refresh outcome unknown; run login before another request")
	}
	if !force && cfg.AccessToken != "" && time.Until(cfg.AccessExpiry) > 30*time.Second {
		auth, err := p.client.VerifyAccess(ctx, cfg.AccessToken, cfg.OAuthIssuer, cfg.Resource)
		if err == nil {
			p.cfg = cfg
			return auth, nil
		}
		var api *community.APIError
		if !errors.As(err, &api) || (api.Status != 401 && api.Status != 403) {
			return community.ExchangeResult{}, err
		}
	}
	// A lost response or crash must never replay an already spent refresh token.
	cfg.RefreshUncertain = true
	if err := saveConfig(p.path, cfg); err != nil {
		return community.ExchangeResult{}, err
	}
	tokens, err := p.client.Refresh(ctx, cfg.APIKey, cfg.Resource)
	if err != nil {
		return community.ExchangeResult{}, err
	}
	storeTokens(&cfg, tokens)
	// Persist the new refresh credential before any subsequent network call.
	if err := saveConfig(p.path, cfg); err != nil {
		return community.ExchangeResult{}, err
	}
	p.cfg = cfg
	return p.client.VerifyAccess(ctx, cfg.AccessToken, cfg.OAuthIssuer, cfg.Resource)
}

func (p *credentialProvider) logout(ctx context.Context) error {
	if strings.HasPrefix(p.cfg.APIKey, "stai_") {
		return errors.New("manual keys are managed in the website; logout requires OAuth config")
	}
	lock, err := lockConfig(ctx, p.path)
	if err != nil {
		return err
	}
	defer closeConfigLock(lock)
	cfg, err := readConfig(p.path)
	if err != nil || !strings.HasPrefix(cfg.APIKey, "stro_") || cfg.BackendURL != p.cfg.BackendURL {
		return errors.New("OAuth config changed; retry logout")
	}
	if err := p.client.Revoke(ctx, cfg.APIKey); err != nil {
		return err
	}
	cfg.APIKey, cfg.AccessToken, cfg.OAuthIssuer, cfg.Resource = "", "", "", ""
	cfg.AccessExpiry = time.Time{}
	cfg.RefreshUncertain = false
	return saveConfig(p.path, cfg)
}
