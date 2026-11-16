package assets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"starry-cli/pkg/transport"
)

func TestSDKReadingLimitsErrorsAndFixedRevision(t *testing.T) {
	revision := "12345678-1234-4234-8234-123456789abc"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer delegated" {
			t.Error("wrong credential")
		}
		if r.URL.Query().Get("revision") != revision {
			t.Error("lost fixed revision")
		}
		if r.URL.Query().Get("cursor") == "changed" {
			w.WriteHeader(409)
			w.Write([]byte(`{"success":false,"error":{"code":"content_changed"}}`))
			return
		}
		if r.URL.Query().Get("cursor") == "bad-page" {
			w.Write([]byte(`{"success":true,"data":{"items":[],"page":{"hasMore":true,"nextCursor":"","endReason":"limit"}}}`))
			return
		}
		if r.URL.Query().Get("cursor") == "failure" {
			w.WriteHeader(503)
			w.Write([]byte(`{"success":false,"error":{"code":"upstream_unavailable"}}`))
			return
		}
		w.Write([]byte(`{"success":true,"data":{"fields":[{"path":"body","encoding":"text","length":10001}],"items":[{"text":"中🙂"}],"page":{"hasMore":true,"nextCursor":"next","endReason":"limit"}}}`))
	}))
	defer server.Close()
	client, _ := transport.NewClient(transport.Config{BackendURL: server.URL})
	input := PackageInput{ResourceInput: ResourceInput{Type: "character", Owner: "1000", Slug: "dragon", Revision: revision}, Profile: "full_package"}
	for _, cursor := range []string{"", "changed", "bad-page", "failure"} {
		input.Cursor = cursor
		data, _ := json.Marshal(input)
		result, err := Call(context.Background(), client, "delegated", "assets_get_package", data)
		if cursor == "" {
			if err != nil || !strings.Contains(string(result["data"].(json.RawMessage)), "next") {
				t.Fatal(result, err)
			}
			continue
		}
		if err == nil {
			t.Fatal("invalid page or backend error treated as empty", cursor)
		}
		api, ok := err.(*transport.APIError)
		if !ok {
			t.Fatal(err)
		}
		if cursor == "changed" && (api.Code != "content_changed" || api.Retryable) {
			t.Fatal(api)
		}
	}
	for _, invalid := range []string{
		`{"type":"character","owner":"1000","slug":"../../private"}`,
		`{"type":"character","owner":"1000","slug":"dragon","ownerUserId":"forged"}`,
		`{"type":"character","owner":"1000","slug":"dragon","bodyLimit":999}`,
		`{"type":"character","owner":"1000","slug":"dragon","group":"delivery","fileRefId":"raw-hash"}`,
	} {
		if _, err := Call(context.Background(), client, "delegated", "assets_get_package", json.RawMessage(invalid)); err == nil {
			t.Fatal("accepted invalid input", invalid)
		}
	}
}

func TestSDKLineageUsesSingleBoundedReadAndPreservesCursorErrors(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/reading/assets/character/1000/dragon/lineage" ||
			r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("revision") != "" {
			t.Error("lineage address or page parameters changed", r.URL)
		}
		if r.URL.Query().Get("cursor") == "expired" {
			w.WriteHeader(http.StatusGone)
			w.Write([]byte(`{"success":false,"error":{"code":"cursor_expired"}}`))
			return
		}
		w.Write([]byte(`{"success":true,"data":{"current":{"id":"root"},"items":[{"id":"child"}],"page":{"hasMore":true,"nextCursor":"next","endReason":"limit"}}}`))
	}))
	defer server.Close()
	client, _ := transport.NewClient(transport.Config{BackendURL: server.URL})
	for _, cursor := range []string{"", "expired"} {
		data, _ := json.Marshal(map[string]any{"type": "character", "owner": "1000", "slug": "dragon", "limit": 2, "cursor": cursor})
		result, err := Call(context.Background(), client, "delegated", "assets_list_lineage", data)
		if cursor == "" && (err != nil || result == nil) {
			t.Fatal(result, err)
		}
		if cursor != "" {
			api, ok := err.(*transport.APIError)
			if !ok || api.Code != "cursor_expired" {
				t.Fatal("expired cursor became an empty success", err)
			}
		}
	}
	if calls != 2 {
		t.Fatal("lineage auto-expanded or auto-restarted", calls)
	}
	for _, invalid := range []string{
		`{"type":"live2d","owner":"1000","slug":"dragon"}`,
		`{"type":"character","owner":"1000","slug":"dragon","limit":21}`,
		`{"type":"character","owner":"1000","slug":"dragon","revision":"12345678-1234-4234-8234-123456789abc"}`,
	} {
		if _, err := Call(context.Background(), client, "delegated", "assets_list_lineage", json.RawMessage(invalid)); err == nil {
			t.Fatal("invalid lineage accepted", invalid)
		}
	}
	if calls != 2 {
		t.Fatal("invalid input reached backend", calls)
	}
	limit := 21
	if _, err := PackagePath(PackageInput{ResourceInput: ResourceInput{Type: "worldbook", Owner: "1000", Slug: "book"}, Group: "entries", Limit: &limit}); err == nil {
		t.Fatal("machine entry limit exceeded 20")
	}
}
