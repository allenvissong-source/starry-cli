package assets

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"starry-cli/pkg/transport"
)

func TestCollaborationPreservesIntentAndDisallowsUnknownFields(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/authoring/assets/character/collaboration/suggestion-adopt" || r.Header.Get("Idempotency-Key") != "unchanged" {
			t.Error("intent route/key changed")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["publishAs"] != "owner" || body["expectedRevision"] != "12345678-1234-4234-8234-123456789abc" || body["type"] != nil {
			t.Error("intent content changed")
		}
		w.Write([]byte(`{"success":true,"data":{"operationId":"same","status":"succeeded"}}`))
	}))
	defer server.Close()
	client, _ := transport.NewClient(transport.Config{BackendURL: server.URL})
	input := `{"type":"character","owner":"1000","slug":"card","requestKey":"unchanged","publishAs":"owner","expectedRevision":"12345678-1234-4234-8234-123456789abc","suggestionId":"12345678-1234-4234-8234-123456789abd"}`
	if _, err := Collaboration(context.Background(), client, "delegate", "assets_adopt_suggestion", json.RawMessage(input)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(input, `"character"`, `"worldbook/escape"`, 1), strings.Replace(input, `"publishAs"`, `"fakeAuthor"`, 1)} {
		if _, err := Collaboration(context.Background(), client, "delegate", "assets_adopt_suggestion", json.RawMessage(bad)); err == nil {
			t.Fatal("invalid input sent")
		}
	}
	if calls != 1 {
		t.Fatal("invalid request reached backend")
	}
}

func TestAcquireUsesCompleteGraphIntentAndRejectsForkOnlyFields(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/authoring/assets/collection/collaboration/acquire" ||
			r.Header.Get("Idempotency-Key") != "acquire-set" {
			t.Error("wrong acquire route or key")
		}
		w.Write([]byte(`{"success":true,"data":{"operationId":"operation","result":{"state":"saved","rootResourceId":"root"}}}`))
	}))
	defer server.Close()
	client, _ := transport.NewClient(transport.Config{BackendURL: server.URL})
	input := `{"type":"collection","owner":"1000","slug":"set","requestKey":"acquire-set","expectedRevision":"12345678-1234-4234-8234-123456789abc","planHash":"` + strings.Repeat("a", 64) + `"}`
	if !IsCollaboration("assets_acquire") {
		t.Fatal("acquire not dispatched")
	}
	if _, err := Collaboration(context.Background(), client, "delegate", "assets_acquire", json.RawMessage(input)); err != nil {
		t.Fatal(err)
	}
	bad := strings.TrimSuffix(input, "}") + `,"draftId":"12345678-1234-4234-8234-123456789abd"}`
	if _, err := Collaboration(context.Background(), client, "delegate", "assets_acquire", json.RawMessage(bad)); err == nil {
		t.Fatal("acquire accepted fork-only field")
	}
	readonly := strings.TrimSuffix(input, "}") + `,"mode":"read_only"}`
	if _, err := Collaboration(context.Background(), client, "delegate", "assets_acquire", json.RawMessage(readonly)); err != nil {
		t.Fatal("read-only acquisition rejected", err)
	}
	unknown := strings.TrimSuffix(input, "}") + `,"mode":"public_copy"}`
	if _, err := Collaboration(context.Background(), client, "delegate", "assets_acquire", json.RawMessage(unknown)); err == nil {
		t.Fatal("unknown acquisition mode accepted")
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestCollaborationLostResponseKeepsOriginalOperationIntent(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("action") != "assets.character.suggestion-adopt" ||
				r.URL.Query().Get("requestKey") != "original-adoption" {
				t.Error("operation query lost original intent")
			}
			w.Write([]byte(`{"success":true,"data":{"operationId":"accepted","status":"succeeded"}}`))
			return
		}
		writes.Add(1)
		if r.Header.Get("Idempotency-Key") != "original-adoption" {
			t.Error("write key changed")
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	}))
	defer server.Close()
	client, _ := transport.NewClient(transport.Config{BackendURL: server.URL})
	input := json.RawMessage(`{"type":"character","owner":"1000","slug":"card","requestKey":"original-adoption","publishAs":"owner","expectedRevision":"12345678-1234-4234-8234-123456789abc","suggestionId":"12345678-1234-4234-8234-123456789abd"}`)
	_, err := Collaboration(context.Background(), client, "delegate", "assets_adopt_suggestion", input)
	var api *transport.APIError
	if !errors.As(err, &api) || api.Code != "outcome_unknown" || api.Retryable || writes.Load() != 1 {
		t.Fatal("lost response automatically retried or lost its state", err, writes.Load())
	}
	if _, err := transport.ReadingResult(client.Request(context.Background(), http.MethodGet,
		"/api/v1/authoring/operations?action=assets.character.suggestion-adopt&requestKey=original-adoption", "delegate", nil)); err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 1 {
		t.Fatal("operation lookup submitted another adoption")
	}
}

func TestAcquisitionPlanAndCommitPreserveExplicitEmptySelection(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		selection, ok := body["selection"].([]any)
		if !ok || len(selection) != 0 || body["strategy"] != "copy" || body["mode"] != "read_only" {
			t.Fatal("explicit choices changed", body)
		}
		if calls == 1 {
			if r.URL.Path != "/api/v1/authoring/assets/collection/acquisition-plan" ||
				r.Header.Get("Idempotency-Key") != "" {
				t.Fatal("plan wrote an operation", r.URL.Path)
			}
		} else if r.URL.Path != "/api/v1/authoring/assets/collection/collaboration/acquire" ||
			r.Header.Get("Idempotency-Key") != "same-intent" || body["planHash"] != strings.Repeat("a", 64) {
			t.Fatal("commit lost its confirmed intent", body)
		}
		w.Write([]byte(`{"success":true,"data":{"plan_hash":"plan","state":"saved"}}`))
	}))
	defer server.Close()
	client, _ := transport.NewClient(transport.Config{BackendURL: server.URL})
	plan := `{"type":"collection","owner":"1000","slug":"set","revision":"12345678-1234-4234-8234-123456789abc","selection":[],"strategy":"copy","mode":"read_only"}`
	if _, err := Collaboration(context.Background(), client, "delegate", "assets_plan_acquisition", json.RawMessage(plan)); err != nil {
		t.Fatal(err)
	}
	commit := strings.Replace(plan, `"revision":`, `"expectedRevision":`, 1)
	commit = strings.TrimSuffix(commit, "}") + `,"requestKey":"same-intent","planHash":"` + strings.Repeat("a", 64) + `"}`
	if _, err := Collaboration(context.Background(), client, "delegate", "assets_acquire", json.RawMessage(commit)); err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(commit, `"planHash":"`+strings.Repeat("a", 64)+`"`, `"planHash":""`, 1)
	if _, err := Collaboration(context.Background(), client, "delegate", "assets_acquire", json.RawMessage(bad)); err == nil {
		t.Fatal("unreviewed acquisition reached the API")
	}
	if calls != 2 {
		t.Fatal("invalid intent reached the API", calls)
	}
}
