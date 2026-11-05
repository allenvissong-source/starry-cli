package community

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResourceWatchLiveGrantAndDomainIsolation(t *testing.T) {
	polls, exchanges, events := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls++
		if r.URL.Path != "/api/v1/reading/assets/inbox" {
			t.Error("wrong inbox")
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"cursor": "state"}})
	}))
	defer server.Close()
	client, _ := NewClient(Config{BackendURL: server.URL})
	principal := Principal{OwnerUserID: "owner", IntegrationID: "connection", CredentialID: "key", GrantVersion: 1, Scopes: []string{"assets.character.notifications.read"}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := WatchDomain(ctx, "assets", "", time.Millisecond, func(context.Context) (ExchangeResult, error) {
		exchanges++
		p := principal
		if exchanges == 3 {
			p.GrantVersion++
		}
		return ExchangeResult{Token: "delegate", Principal: p}, nil
	}, client, principal, func(event InboxEvent) error {
		events++
		if event.URI != AssetsInboxURI || event.Refresh != "assets_list_notifications" {
			t.Error("domain wrong")
		}
		return nil
	})
	var api *APIError
	if !errors.As(err, &api) || api.Status != 403 || polls != 2 || events != 1 {
		t.Fatal(err, polls, events)
	}
}
