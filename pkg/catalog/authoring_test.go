package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/allenvissong-source/starry-cli/pkg/transport"
)

func TestCompleteSkillInventoriesUseBackendSpecificBounds(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.ContentLength <= 64<<10 || r.ContentLength > 4<<20 {
			t.Error("unexpected Skill body bound")
		}
		w.Write([]byte(`{"success":true,"data":{"result":{"state":"saved"}}}`))
	}))
	defer server.Close()
	client, _ := transport.NewClient(transport.Config{BackendURL: server.URL})
	payload := `{"requestKey":"large-skill","fields":{"files":[{"path":"references/long.md","media_type":"text/markdown","content":"` + strings.Repeat("YWJj", 32<<10) + `"}]}}`
	for _, name := range []string{"catalog_create_skill", "catalog_update_skill", "catalog_create_skill_binding",
		"catalog_update_skill_binding", "catalog_apply_character_skills"} {
		if _, err := Call(context.Background(), client, "delegate", name, json.RawMessage(payload)); err != nil {
			t.Fatal(name, err)
		}
	}
	for _, name := range []string{"catalog_update_character", "catalog_apply_collection_items", "catalog_delete_skill"} {
		if _, err := Call(context.Background(), client, "delegate", name, json.RawMessage(payload)); err == nil {
			t.Fatal("ordinary action enlarged", name)
		}
	}
	if _, err := Call(context.Background(), client, "delegate", "catalog_create_skill",
		json.RawMessage(`{"requestKey":"oversized","fields":{"summary":"`+strings.Repeat("x", 4<<20)+`"}}`)); err == nil {
		t.Fatal("oversized complete Skill intent accepted")
	}
	if calls != 5 {
		t.Fatal("rejected requests reached backend", calls)
	}
}

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

func TestUseAccessIsAuthenticatedReadWithoutMutationKey(t *testing.T) {
	testAccessIsAuthenticatedReadWithoutMutationKey(t, "use")
}

func TestEditAccessIsAuthenticatedReadWithoutMutationKey(t *testing.T) {
	testAccessIsAuthenticatedReadWithoutMutationKey(t, "edit")
}

func testAccessIsAuthenticatedReadWithoutMutationKey(t *testing.T, purpose string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/reading/catalog/skills/12345678-1234-4234-8234-123456789abc/"+purpose+"-access" ||
			r.Header.Get("Authorization") != "Bearer delegate" || r.Header.Get("Idempotency-Key") != "" {
			t.Fatalf("wrong use-access request: %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"success":true,"data":{"allowed":true,"resources":[{"type":"skill","id":"12345678-1234-4234-8234-123456789abc"}]}}`))
	}))
	defer server.Close()
	client, _ := transport.NewClient(transport.Config{BackendURL: server.URL})
	if _, err := Call(context.Background(), client, "delegate", "catalog_check_"+purpose+"_access",
		json.RawMessage(`{"type":"skill","id":"12345678-1234-4234-8234-123456789abc"}`)); err != nil {
		t.Fatal(err)
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

func TestSkillFilesAndCollectionChangesRetainAtomicInput(t *testing.T) {
	for _, test := range []struct{ name, action, input string }{
		{"catalog_update_skill", "skill.update", `{"requestKey":"files-intent","id":"12345678-1234-4234-8234-123456789abc","expectedVersion":3,"expectedReadingVersion":"5","fields":{"files":[{"path":"SKILL.md","media_type":"text/markdown","content":"YWJj"}]}}`},
		{"catalog_apply_character_skills", "skill_binding.apply", `{"requestKey":"files-intent","parentId":"12345678-1234-4234-8234-123456789abc","expectedReadingVersion":"5","changes":[{"action":"create","id":"12345678-1234-4234-8234-123456789abd","fields":{"skill_id":"12345678-1234-4234-8234-123456789abe","activation":"manual"}}]}`},
		{"catalog_apply_collection_items", "collection_item.apply", `{"requestKey":"files-intent","parentId":"12345678-1234-4234-8234-123456789abc","expectedReadingVersion":"5","changes":[{"action":"delete","id":"12345678-1234-4234-8234-123456789abd","expectedVersion":2}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/authoring/catalog/"+test.action || r.Header.Get("Idempotency-Key") != "files-intent" {
					t.Error("lost stable action or key")
				}
				var actual, wanted map[string]any
				json.NewDecoder(r.Body).Decode(&actual)
				json.Unmarshal([]byte(test.input), &wanted)
				a, _ := json.Marshal(actual)
				b, _ := json.Marshal(wanted)
				if string(a) != string(b) {
					t.Fatalf("input changed: %s", a)
				}
				w.Write([]byte(`{"success":true,"data":{"result":{"state":"saved"}}}`))
			}))
			defer server.Close()
			client, _ := transport.NewClient(transport.Config{BackendURL: server.URL})
			if _, err := Call(context.Background(), client, "delegate", test.name, json.RawMessage(test.input)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
