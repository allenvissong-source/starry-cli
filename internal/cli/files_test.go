package cli

import (
	"archive/zip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allenvissong-source/starry-cli/pkg/transport"
)

func TestPublishedMediaSlotsUseLogicalPathAndDeliveryRoleTogether(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for slot, delivery := range map[string]string{
		"avatar": "preview", "cover": "cover", "illustration": "file", "background": "file",
	} {
		file := map[string]any{"logical_path": slot + "/" + hash, "reference_role": delivery, "sha256": hash}
		actual, err := publishedMediaSlot(file)
		if err != nil || actual != slot {
			t.Fatal(slot, actual, err)
		}
		file["reference_role"] = "archive"
		if _, err := publishedMediaSlot(file); err == nil {
			t.Fatal("mismatched delivery role accepted")
		}
		file["reference_role"], file["logical_path"] = delivery, slot+"/"+strings.Repeat("b", 64)
		if _, err := publishedMediaSlot(file); err == nil {
			t.Fatal("mismatched logical content address accepted")
		}
	}
	if slot, err := publishedMediaSlot(map[string]any{
		"logical_path": "cover_asset_id", "reference_role": "cover", "sha256": hash,
	}); err != nil || slot != "cover" {
		t.Fatal("existing pre-preset cover contract", slot, err)
	}
}

func TestFullUnicodeFieldCollectionRequiresValidOffsetsAndTermination(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			offset, value, more, cursor := 0, "龙", true, "next"
			if r.URL.Query().Get("cursor") == "next" {
				offset, value, more, cursor = 1, "🌟", false, ""
				if invalid {
					offset = 2
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
				"readingVersion": "pinned", "fields": []any{map[string]any{"path": "content", "encoding": "text", "length": 2}},
				"items": []any{map[string]any{"path": "content", "encoding": "text", "length": 2, "offset": offset, "text": value}},
				"page":  map[string]any{"hasMore": more, "nextCursor": cursor, "endReason": map[bool]string{true: "limit", false: "complete"}[more]}}})
		}))
		client, _ := transport.NewClient(transport.Config{BackendURL: server.URL})
		_, fields, err := readContent(context.Background(), client, "token", "/api/v1/content", nil)
		if invalid && err == nil || !invalid && (err != nil || fields["content"] != "龙🌟") {
			t.Fatal(fields, err)
		}
		server.Close()
	}
}

func TestImportResumeQueriesOriginalCommittedIntentAndNeverRetriesUnknown(t *testing.T) {
	ctx := context.Background()
	writes, reads := 0, 0
	confirmed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			writes++
			w.WriteHeader(503)
			w.Write([]byte(`{"success":false,"error":{"code":"upstream_unavailable"}}`))
			return
		}
		reads++
		if r.URL.Query().Get("action") != "character.create" || r.URL.Query().Get("requestKey") != "original" {
			t.Error("changed recovery selector")
		}
		if !confirmed {
			w.WriteHeader(404)
			w.Write([]byte(`{"success":false,"error":{"code":"not_found"}}`))
		} else {
			w.Write([]byte(`{"success":true,"data":{"operationId":"same-operation","result":{"id":"same-id"}}}`))
		}
	}))
	defer server.Close()
	client, _ := transport.NewClient(transport.Config{BackendURL: server.URL})
	path := filepath.Join(t.TempDir(), "progress.json")
	p := importProgress{Version: 1, Binding: "same-connection", Type: "character", Key: "file-intent",
		Media: map[string]string{}, Graph: fileGraph{Character: map[string]any{"id": "12345678-1234-4234-8234-123456789abc"}}}
	p.Steps, _ = normalizeGraph(&fileGraph{Character: map[string]any{"id": "source", "name": "card"}}, "character", "seed")
	p.Steps[0].Input.RequestKey = "original"
	if err := saveProgress(path, p); err != nil {
		t.Fatal(err)
	}
	in := fileInput{File: "immutable-source.json", Progress: path, RequestKey: "file-intent"}
	if _, err := importFile(ctx, client, "token", "same-connection", "character", in); err == nil {
		t.Fatal("lost unknown outcome")
	}
	if _, err := importFile(ctx, client, "token", "same-connection", "character", in); err == nil {
		t.Fatal("missing treated as failed write")
	}
	if writes != 1 || reads != 1 {
		t.Fatal("unknown intent automatically retried", writes, reads)
	}
	confirmed = true
	if out, err := importFile(ctx, client, "token", "same-connection", "character", in); err != nil || out["data"] == nil {
		t.Fatal(out, err)
	}
	if writes != 1 || reads != 2 {
		t.Fatal(writes, reads)
	}
	if _, err := importFile(ctx, client, "token", "other-connection", "character", in); err == nil {
		t.Fatal("cross-connection progress reused")
	}
}

func TestHostArchivePreflightRejectsTraversalDuplicateAndSymlink(t *testing.T) {
	for _, attack := range []string{"../escape", "/absolute", "duplicate", "symlink", "back\\slash", "a/../b"} {
		path := filepath.Join(t.TempDir(), "card.starrry")
		path = strings.TrimSuffix(path, ".starrry") + ".starry"
		f, _ := os.Create(path)
		z := zip.NewWriter(f)
		name := attack
		header := &zip.FileHeader{Name: name}
		if attack == "symlink" {
			header.SetMode(os.ModeSymlink | 0777)
		}
		w, _ := z.CreateHeader(header)
		w.Write([]byte("payload"))
		if attack == "duplicate" {
			w, _ = z.Create(attack)
			w.Write([]byte("another"))
		}
		z.Close()
		f.Close()
		if _, err := validateSource(path, "character"); err == nil {
			t.Fatal("accepted", attack)
		}
	}
}

func TestImportedGraphRemapsIdentifiersAndPreservesEntryIDsAndAllPositions(t *testing.T) {
	positions := []string{"before_char", "after_char", "before_author_note", "after_author_note", "at_depth", "before_example", "after_example", "outlet"}
	entries := []any{}
	for i, position := range positions {
		entries = append(entries, map[string]any{"id": float64(i + 5), "uid": position, "position": position, "outlet_name": "出口", "content": "龙🌟", "keys": []any{"key"}})
	}
	graph := fileGraph{Character: map[string]any{"id": "external-card", "name": "card"},
		Worldbooks: []map[string]any{{"id": "external-book", "name": "book", "entries": entries}},
		Bindings:   []map[string]any{{"worldbook_id": "external-book", "reference_type": "embedded"}}}
	steps, err := normalizeGraph(&graph, "character", "seed")
	if err != nil || len(steps) != 11 {
		t.Fatal(len(steps), err)
	}
	for i, step := range steps[2:10] {
		if step.Input.ParentID == "external-book" || step.Input.Fields["position"] != positions[i] ||
			asObject(asObject(step.Input.Fields["extensions"])["starry_file_source"])["numeric_id"] != float64(i+5) ||
			step.Input.Fields["outlet_name"] != "出口" {
			t.Fatal(step)
		}
	}
	if steps[10].Input.Fields["worldbook_id"] != graph.Worldbooks[0]["id"] {
		t.Fatal("binding not remapped")
	}
}

func TestCollectionImportRemapsSharedSkillAndDisabledBindings(t *testing.T) {
	g := fileGraph{
		Collection: map[string]any{"id": "set", "name": "Set"},
		Characters: []map[string]any{{"id": "a", "name": "A"}, {"id": "b", "name": "B"}},
		Skills:     []map[string]any{{"id": "s", "display_name": "Role", "files": []any{}}},
		SkillBindings: []map[string]any{
			{"character_id": "a", "skill_id": "s", "enabled": false, "activation": "manual", "required": true},
			{"character_id": "b", "skill_id": "s", "enabled": true, "activation": "always"},
		},
		CollectionItems: []map[string]any{
			{"target_type": "character", "target_id": "a", "sort_order": 1, "group": "Roles", "note": "First"},
			{"target_type": "character", "target_id": "b", "sort_order": 2},
		},
		AcquisitionOrigins: map[string]map[string]any{"s": {
			"owner_user_id": "owner", "resource_type": "skill", "resource_id": "12345678-1234-4234-8234-123456789abc",
		}},
	}
	steps, err := normalizeGraph(&g, "collection", "seed")
	if err != nil {
		t.Fatal(err)
	}
	creates, bindings, items := 0, 0, 0
	for _, step := range steps {
		switch step.Name {
		case "catalog_create_skill":
			creates++
			if step.Input.AcquisitionOrigin["resource_type"] != "skill" {
				t.Fatal("lost origin")
			}
		case "catalog_create_skill_binding":
			bindings++
			if step.Input.Fields["skill_id"] != g.Skills[0]["id"] {
				t.Fatal("shared target not remapped")
			}
			if step.Input.ParentID == g.Characters[0]["id"] && (step.Input.Fields["enabled"] != false || step.Input.Fields["activation"] != "manual" || step.Input.Fields["required"] != true) {
				t.Fatal("disabled binding changed", step)
			}
		case "catalog_apply_collection_items":
			items++
			if step.Input.ParentID != g.Collection["id"] || len(step.Input.Changes) != 1 {
				t.Fatal("member parent lost", step)
			}
		}
	}
	if creates != 1 || bindings != 2 || items != 2 {
		t.Fatal(creates, bindings, items)
	}
}

func TestStandaloneImportRejectsUnrelatedRootsBeforeWrites(t *testing.T) {
	for _, kind := range []string{"character", "worldbook", "skill"} {
		g := fileGraph{
			Character:  map[string]any{"id": "c", "name": "C"},
			Worldbooks: []map[string]any{{"id": "w", "name": "W"}},
			Skills:     []map[string]any{{"id": "s", "display_name": "S"}},
			Collection: map[string]any{"id": "set", "name": "Set"},
		}
		if _, err := normalizeGraph(&g, kind, "intent"); err == nil {
			t.Fatal("unrelated graph accepted", kind)
		}
	}
	if _, err := normalizeGraph(&fileGraph{}, "character", "intent"); err == nil {
		t.Fatal("missing character accepted")
	}
}

func TestCollectionMediaUsesDistinctIntentPerCharacter(t *testing.T) {
	hash := strings.Repeat("a", 64)
	g := fileGraph{
		Collection: map[string]any{"id": "set", "name": "Set"},
		Characters: []map[string]any{{"id": "a", "name": "A"}, {"id": "b", "name": "B"}},
		CollectionItems: []map[string]any{
			{"target_type": "character", "target_id": "a"},
			{"target_type": "character", "target_id": "b"},
		},
		Media: []map[string]any{{"sha256": hash}},
		Relations: []map[string]any{
			{"character_id": "a", "target_type": "asset", "target_id": hash, "role": "avatar"},
			{"character_id": "b", "target_type": "asset", "target_id": hash, "role": "avatar"},
		},
	}
	steps, err := normalizeGraph(&g, "collection", "intent")
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, step := range steps {
		if keys[step.Input.RequestKey] {
			t.Fatal("two graph writes share an intent", step.Name)
		}
		keys[step.Input.RequestKey] = true
	}
}
