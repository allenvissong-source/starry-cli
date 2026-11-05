package community

import (
	"context"
	"encoding/json"
	"slices"
	"time"
)

const InboxURI = "starry://community/inbox"
const AssetsInboxURI = "starry://assets/inbox"

type InboxEvent struct {
	Type    string `json:"type"`
	URI     string `json:"uri"`
	Cursor  string `json:"cursor"`
	Refresh string `json:"refresh"`
}

// Watch 对持久收件箱版本去重。它发送合并的失效信号，不保证每条事件单独交付。
// 首轮等一个周期，以便 MCP 的 subscriptions/listen 完成注册和 acknowledged。
func Watch(ctx context.Context, cursor string, interval time.Duration, exchange func(context.Context) (ExchangeResult, error), client *Client, principal Principal, emit func(InboxEvent) error) error {
	return WatchDomain(ctx, "community", cursor, interval, exchange, client, principal, emit)
}

func WatchDomain(ctx context.Context, domain, cursor string, interval time.Duration, exchange func(context.Context) (ExchangeResult, error), client *Client, principal Principal, emit func(InboxEvent) error) error {
	path, uri, refresh := "/api/v1/community/inbox", InboxURI, "community_list_notifications"
	authorized := func(scopes []string) bool { return slices.Contains(scopes, "community.read") }
	if domain == "assets" {
		path, uri, refresh = "/api/v1/reading/assets/inbox", AssetsInboxURI, "assets_list_notifications"
		authorized = func(scopes []string) bool {
			return slices.Contains(scopes, "assets.character.notifications.read") || slices.Contains(scopes, "assets.worldbook.notifications.read")
		}
	} else if domain != "community" {
		return newAPIError(400, "invalid_argument")
	}
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	first := true
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		auth, err := exchange(ctx)
		if err != nil {
			return err
		}
		if auth.Principal.OwnerUserID != principal.OwnerUserID || auth.Principal.IntegrationID != principal.IntegrationID ||
			auth.Principal.CredentialID != principal.CredentialID || !slices.Equal(auth.Principal.Scopes, principal.Scopes) ||
			auth.Principal.GrantVersion != principal.GrantVersion ||
			!authorized(auth.Principal.Scopes) {
			return newAPIError(403, "integration.forbidden")
		}
		data, err := client.Request(ctx, "GET", path, auth.Token, nil)
		if err != nil {
			return err
		}
		var state struct {
			Cursor string `json:"cursor"`
		}
		if json.Unmarshal(data, &state) != nil || state.Cursor == "" {
			return newAPIError(503, "upstream_unavailable")
		}
		if !first && state.Cursor == cursor {
			continue
		}
		if err := emit(InboxEvent{Type: "inbox_updated", URI: uri, Cursor: state.Cursor, Refresh: refresh}); err != nil {
			return err
		}
		cursor = state.Cursor
		first = false
	}
}
