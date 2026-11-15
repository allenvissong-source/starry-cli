package community

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSharedPublishPreservesIDsAndUnknownIntent(t *testing.T) {
	var payload string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Idempotency-Key") != "same-intent" {
			t.Error("intent key changed")
		}
		var body map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&body)
		payload = string(body["entityId"])
		if _, exists := body["requestKey"]; exists {
			t.Error("requestKey leaked into form")
		}
		if string(body["content"]) == `"unknown"` {
			w.WriteHeader(503)
			w.Write([]byte(`{"success":false,"error":{"code":"upstream_unavailable"}}`))
			return
		}
		w.Write([]byte(`{"success":true,"data":{"action":"article.create","entityId":"9007199254740993","operationId":"op"}}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{BackendURL: server.URL, PublicWebURL: "https://starry.example"})
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"publishAs":"owner","requestKey":"same-intent","entityType":"article","entityId":"9007199254740993","content":"ok"}`)
	out, err := client.Call(context.Background(), "token", "community_create_comment", input)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(out)
	if payload != `"9007199254740993"` || !strings.Contains(string(data), "/article/9007199254740993") {
		t.Fatal(payload, string(data))
	}
	input = json.RawMessage(strings.ReplaceAll(string(input), `"ok"`, `"unknown"`))
	out, err = client.Call(context.Background(), "token", "community_create_comment", input)
	if err != nil || out["state"] != "outcome_unknown" || out["requestKey"] != "same-intent" {
		t.Fatal(out, err)
	}
	for _, input := range []string{`null`, `{} {}`, `{"admin":true}`, `{"entityId":9007199254740993.1}`} {
		if _, err := client.Call(context.Background(), "token", "community_create_comment", json.RawMessage(input)); err == nil {
			t.Fatal("invalid input accepted", input)
		}
	}
}

func TestWatchCoalescesAndRechecksAuth(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"data":{"cursor":"new-revision"}}`))
	}))
	defer server.Close()
	client, _ := NewClient(Config{BackendURL: server.URL})
	principal := Principal{OwnerUserID: "a", Scopes: []string{"community.read"}}
	events := 0
	err := Watch(context.Background(), "old", 10*time.Millisecond, func(context.Context) (ExchangeResult, error) {
		calls++
		if calls == 3 {
			return ExchangeResult{}, newAPIError(401, "integration.unauthorized")
		}
		return ExchangeResult{Token: "token", Principal: principal}, nil
	}, client, principal, func(InboxEvent) error { events++; return nil })
	if err == nil || events != 1 || calls != 3 {
		t.Fatal(events, calls, err)
	}
}

func TestWatchRefreshesUnchangedSavedRevision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"data":{"cursor":"saved"}}`))
	}))
	defer server.Close()
	client, _ := NewClient(Config{BackendURL: server.URL})
	principal := Principal{OwnerUserID: "a", Scopes: []string{"community.read"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := 0
	_ = Watch(ctx, "saved", 10*time.Millisecond, func(context.Context) (ExchangeResult, error) {
		return ExchangeResult{Token: "delegate", Principal: principal}, nil
	}, client, principal, func(InboxEvent) error { events++; cancel(); return nil })
	if events != 1 {
		t.Fatal("unchanged revision skipped initial synchronization", events)
	}
}
