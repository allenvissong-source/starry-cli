package assets

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/allenvissong-source/starry-cli/pkg/transport"
)

func TestPublicationUsesExactInputsAndPreservesUnknownIntent(t *testing.T) {
	ctx := context.Background()
	input := PublicationInput{Type: "character", RequestKey: "publish-intent-1",
		DraftID: "12345678-1234-4234-8234-123456789abc", Slug: "card",
		PublishAs: "ai", AIIdentityID: "approved-ai",
		PreviewID: "12345678-1234-4234-8234-123456789abd",
		Snapshot:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if r.URL.Path != "/api/v1/authoring/assets/character/publish" ||
			r.Header.Get("Idempotency-Key") != input.RequestKey ||
			r.Header.Get("Authorization") != "Bearer delegated" {
			t.Error("publication routing or intent changed")
		}
		var got map[string]any
		json.NewDecoder(r.Body).Decode(&got)
		if got["snapshot"] != input.Snapshot || got["publishAs"] != "ai" ||
			got["aiIdentityId"] != input.AIIdentityID || got["type"] != nil {
			t.Error("publication input not preserved", got)
		}
		if call == 1 {
			w.WriteHeader(201)
			w.Write([]byte(`{"success":true,"data":{"operationId":"op","status":"succeeded","current":{"review_state":"pending"}}}`))
		} else {
			h, _, _ := w.(http.Hijacker).Hijack()
			h.Close()
		}
	}))
	defer server.Close()
	client, _ := transport.NewClient(transport.Config{BackendURL: server.URL})
	raw, _ := json.Marshal(input)
	result, err := Publication(ctx, client, "delegated", "assets_publish", raw)
	if err != nil || result["httpStatus"] != 201 {
		t.Fatal(result, err)
	}
	_, err = Publication(ctx, client, "delegated", "assets_publish", raw)
	var api *transport.APIError
	if !errors.As(err, &api) || api.Code != "outcome_unknown" || api.Retryable || calls.Load() != 2 {
		t.Fatal("uncertain write is retried or misclassified", err, calls.Load())
	}
	for _, invalid := range []string{
		`{"type":"character","requestKey":"x","draftId":"12345678-1234-4234-8234-123456789abc","slug":"card","publishAs":"owner","aiIdentityId":"forged"}`,
		`{"type":"character","requestKey":"x","draftId":"12345678-1234-4234-8234-123456789abc","slug":"card","publishAs":"owner","author":{"name":"forged"}}`,
	} {
		if _, err := Publication(ctx, client, "delegated", "assets_publish", json.RawMessage(invalid)); err == nil {
			t.Fatal("invalid signature accepted")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("invalid input reached backend")
	}
}
