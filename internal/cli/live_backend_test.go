package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func liveCall(t *testing.T, args []string, input string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	if exit := Run(context.Background(), args, strings.NewReader(input), &out, &errOut); exit != 0 {
		t.Fatal(exit, errOut.String(), out.String())
	}
	return out.String()
}

func TestLiveBackendReads(t *testing.T) {
	if os.Getenv("STARRY_E2E_BACKEND") == "" {
		t.Skip("disposable Gin E2E backend not configured")
	}
	t.Setenv("STARRY_BACKEND_URL", os.Getenv("STARRY_E2E_BACKEND"))
	t.Setenv("STARRY_API_KEY", os.Getenv("STARRY_E2E_KEY_A"))
	t.Setenv("STARRY_CLI_CONFIG", "")
	if !strings.Contains(liveCall(t, []string{"identity"}, ""), "user_a") {
		t.Fatal("wrong owner")
	}
	liveCall(t, []string{"list", "--input", "-"}, `{"source":"latest","kind":"all"}`)
	if !strings.Contains(liveCall(t, []string{"post", "--input", "-"}, `{"kind":"dynamic","id":"9007199254740993","includeComments":false}`), "9007199254740993") {
		t.Fatal("high ID corrupted")
	}
	liveCall(t, []string{"inbox"}, "")
	liveCall(t, []string{"watch", "--once", "--timeout", "8s"}, "")
	handler := testHandler(t, "valid")
	configPath := filepath.Join(filepath.Dir(handler.StateFile), "config.json")
	raw, _ := json.Marshal(config{BackendURL: os.Getenv("STARRY_E2E_BACKEND"), APIKey: os.Getenv("STARRY_E2E_KEY_A"), Handler: handler})
	if err := os.WriteFile(configPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	dispatch := []string{"watch", "--config", configPath, "--dispatch", "--once", "--timeout", "30s"}
	first := liveCall(t, dispatch, "")
	if !strings.Contains(first, "task_started") || !strings.Contains(first, "task_completed") {
		t.Fatal("live notification not dispatched", first)
	}
	if again := liveCall(t, dispatch, ""); strings.Contains(again, "task_started") {
		t.Fatal("live notification redispatched after restart", again)
	}
}

func TestLiveBackendWrites(t *testing.T) {
	if os.Getenv("STARRY_E2E_BACKEND") == "" || os.Getenv("STARRY_E2E_WRITE_KEY") == "" {
		t.Skip("disposable Gin write backend not configured")
	}
	t.Setenv("STARRY_BACKEND_URL", os.Getenv("STARRY_E2E_BACKEND"))
	t.Setenv("STARRY_API_KEY", os.Getenv("STARRY_E2E_WRITE_KEY"))
	t.Setenv("STARRY_CLI_CONFIG", "")
	for _, row := range []struct{ command, action, input string }{
		{"create-dynamic", "dynamic.create", `{"requestKey":"cli-dynamic","publishAs":"ai","aiIdentityId":"` + os.Getenv("STARRY_E2E_AI_ID") + `","nodeId":"1","content":"CLI dynamic"}`},
		{"create-article", "article.create", `{"requestKey":"cli-article","publishAs":"owner","nodeId":"1","title":"CLI article","content":"body"}`},
		{"create-comment", "comment.create", `{"requestKey":"cli-comment","publishAs":"owner","entityType":"dynamic","entityId":"100","content":"CLI comment"}`},
	} {
		first := liveCall(t, []string{row.command, "--input", "-"}, row.input)
		replay := liveCall(t, []string{row.command, "--input", "-"}, row.input)
		if first != replay || !strings.Contains(first, "pending_review") {
			t.Fatal("retry not idempotent", first, replay)
		}
		var input struct {
			RequestKey string `json:"requestKey"`
		}
		json.Unmarshal([]byte(row.input), &input)
		query, _ := json.Marshal(map[string]string{"action": row.action, "requestKey": input.RequestKey})
		if liveCall(t, []string{"operation", "--input", "-"}, string(query)) != first {
			t.Fatal("operation lookup differs")
		}
	}
}
