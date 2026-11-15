package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIDirectExchangeReadPublishAndUnknown(t *testing.T) {
	seen := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		if r.URL.Path == "/api/v1/integration-token" {
			if r.Header.Get("Authorization") != "Bearer stai_test" {
				t.Error("wrong API key")
			}
			w.Write([]byte(`{"success":true,"data":{"token":"delegate","principal":{"ownerUserId":"a","scopes":["community.read"]}}}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer delegate" {
			t.Error("not delegated")
		}
		if r.Method == "POST" {
			if r.Header.Get("Idempotency-Key") != "same-key" {
				t.Error("missing intent key")
			}
			var row map[string]json.RawMessage
			json.NewDecoder(r.Body).Decode(&row)
			if string(row["content"]) == `"unknown"` {
				w.WriteHeader(503)
				w.Write([]byte(`{"success":false,"error":{"code":"upstream_unavailable"}}`))
				return
			}
			w.Write([]byte(`{"success":true,"data":{"action":"dynamic.create","entityId":"9007199254740993"}}`))
			return
		}
		w.Write([]byte(`{"success":true,"data":{"items":[{"id":"9007199254740993"}]}}`))
	}))
	defer server.Close()
	t.Setenv("STARRY_BACKEND_URL", server.URL)
	t.Setenv("STARRY_API_KEY", "stai_test")
	t.Setenv("STARRY_CLI_CONFIG", "")
	for _, test := range []struct {
		args  []string
		input string
		exit  int
	}{
		{[]string{"list"}, "", 0},
		{[]string{"create-dynamic", "--input", "-"}, `{"requestKey":"same-key","publishAs":"owner","content":"approved"}`, 0},
		{[]string{"create-dynamic", "--input", "-"}, `{"requestKey":"same-key","publishAs":"owner","content":"unknown"}`, 2},
	} {
		var out, errOut bytes.Buffer
		exit := Run(context.Background(), test.args, strings.NewReader(test.input), &out, &errOut)
		if exit != test.exit || errOut.Len() != 0 {
			t.Fatal(exit, out.String(), errOut.String())
		}
		if !json.Valid(out.Bytes()) {
			t.Fatal("invalid JSON output", out.String())
		}
	}
	for _, path := range seen {
		if path == "/mcp" || strings.HasPrefix(path, "/internal/") {
			t.Fatal("CLI used MCP/server secret")
		}
	}
}

func TestCredentialConfigAndAtomicCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"backendUrl":"https://example.com","apiKey":"stai_private"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STARRY_BACKEND_URL", "")
	t.Setenv("STARRY_API_KEY", "")
	t.Setenv("STARRY_PUBLIC_WEB_URL", "")
	if _, err := loadConfig(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("public credential file accepted")
	}
	cursorPath := filepath.Join(t.TempDir(), "cursor")
	if err := saveCursor(cursorPath, "one"); err != nil {
		t.Fatal(err)
	}
	if err := saveCursor(cursorPath, "two"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cursorPath)
	if err != nil || string(data) != "two\n" {
		t.Fatal(string(data), err)
	}
	var out, errOut bytes.Buffer
	if Run(context.Background(), []string{"list", "--api-key", "stai_should_not_echo"}, nil, &out, &errOut) != 1 ||
		strings.Contains(errOut.String(), "stai_should_not_echo") {
		t.Fatal("argv credential echoed")
	}
}
