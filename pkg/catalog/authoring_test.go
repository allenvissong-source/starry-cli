package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"starry-cli/pkg/transport"
)

func TestAuthoringSharedRequestPreservesNullAnd201(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/authoring/catalog/character.update" ||
			r.Header.Get("Idempotency-Key") != "intent-1" || r.Header.Get("Authorization") != "Bearer delegate" {
			t.Fatalf("unexpected write %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		fields := body["fields"].(map[string]any)
		if value, present := fields["description"]; !present || value != nil {
			t.Fatal("explicit null lost")
		}
		if _, present := fields["cover_asset_id"]; present {
			t.Fatal("omitted field added")
		}
		w.Header().Set("X-Request-ID", "request-1")
		w.WriteHeader(201)
		w.Write([]byte(`{"success":true,"data":{"operationId":"op","result":{"state":"saved"}}}`))
	}))
	defer server.Close()
	client, err := transport.NewClient(transport.Config{BackendURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Call(context.Background(), client, "delegate", "catalog_update_character",
		json.RawMessage(`{"requestKey":"intent-1","id":"12345678-1234-4234-8234-123456789abc","expectedVersion":1,"expectedReadingVersion":"2","fields":{"description":null}}`))
	if err != nil || out["httpStatus"] != 201 || out["requestId"] != "request-1" {
		t.Fatal(out, err)
	}
}

func TestAuthoringUnknownOutcomeIsNotOrdinaryRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte(`{"success":false,"error":{"code":"upstream_unavailable"}}`))
	}))
	defer server.Close()
	client, _ := transport.NewClient(transport.Config{BackendURL: server.URL})
	_, err := Call(context.Background(), client, "delegate", "catalog_create_character",
		json.RawMessage(`{"requestKey":"intent-1","id":"12345678-1234-4234-8234-123456789abc","fields":{"name":"Draft"}}`))
	api, ok := err.(*transport.APIError)
	if !ok || api.Code != "outcome_unknown" || api.Retryable {
		t.Fatal(err)
	}
}
