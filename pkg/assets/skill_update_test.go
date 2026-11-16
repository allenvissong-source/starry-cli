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

func TestSkillUpdateSourceAndExactAcquisitionChoices(t *testing.T) {
	id := "12345678-1234-4234-8234-123456789abc"
	update := `{"sourceSkillId":"` + id + `","expectedVersion":4,"bindings":[{"id":"` + id + `","expectedVersion":3}]}`
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if calls == 1 {
			if r.URL.Path != "/api/v1/authoring/assets/skill/update-source" || body["id"] != id {
				t.Fatal("source lookup lost owned ID", body)
			}
		} else {
			raw, _ := json.Marshal(body["skillUpdate"])
			var want any
			json.Unmarshal([]byte(update), &want)
			expected, _ := json.Marshal(want)
			if string(raw) != string(expected) {
				t.Fatal("selected binding versions lost", body)
			}
		}
		w.Write([]byte(`{"success":true,"data":{"id":"skill","plan_hash":"hash"}}`))
	}))
	defer server.Close()
	client, _ := transport.NewClient(transport.Config{BackendURL: server.URL})
	if _, err := Collaboration(context.Background(), client, "delegate", "assets_get_skill_update_source", json.RawMessage(`{"id":"`+id+`"}`)); err != nil {
		t.Fatal(err)
	}
	plan := `{"type":"skill","owner":"1000","slug":"role","revision":"` + id + `","skillUpdate":` + update + `}`
	if _, err := Collaboration(context.Background(), client, "delegate", "assets_plan_acquisition", json.RawMessage(plan)); err != nil {
		t.Fatal(err)
	}
	commit := strings.Replace(plan, `"revision":`, `"expectedRevision":`, 1)
	commit = strings.TrimSuffix(commit, "}") + `,"requestKey":"update-role","planHash":"` + strings.Repeat("a", 64) + `"}`
	if _, err := Collaboration(context.Background(), client, "delegate", "assets_acquire", json.RawMessage(commit)); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		strings.Replace(plan, `"type":"skill"`, `"type":"character"`, 1),
		strings.Replace(plan, `"expectedVersion":4`, `"expectedVersion":0`, 1),
		strings.Replace(plan, `"expectedVersion":3`, `"expectedVersion":0`, 1),
		strings.Replace(plan, `"bindings":[{"id":"`+id+`","expectedVersion":3}]`, `"bindings":null`, 1),
	} {
		if _, err := Collaboration(context.Background(), client, "delegate", "assets_plan_acquisition", json.RawMessage(invalid)); err == nil {
			t.Fatal("invalid update reached API", invalid)
		}
	}
	if calls != 3 {
		t.Fatal("invalid update made API request", calls)
	}
}
