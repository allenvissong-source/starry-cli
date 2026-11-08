package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResourceCommandsUseSharedHTTPReading(t *testing.T) {
	lastPath := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/integration-token" {
			w.Write([]byte(`{"success":true,"data":{"token":"delegate","principal":{"ownerUserId":"owner"}}}`))
			return
		}
		lastPath = r.URL.Path
		if r.Header.Get("Authorization") != "Bearer delegate" {
			t.Error("wrong read credential")
		}
		w.Write([]byte(`{"success":true,"data":{"items":[],"page":{"hasMore":false,"nextCursor":"","endReason":"complete"}}}`))
	}))
	defer server.Close()
	t.Setenv("STARRY_BACKEND_URL", server.URL)
	t.Setenv("STARRY_API_KEY", "stai_test")
	t.Setenv("STARRY_CLI_CONFIG", "")
	for _, item := range []struct {
		args        []string
		input, path string
	}{
		{[]string{"asset", "search"}, `{"type":"character","keyword":"龙城"}`, "/api/v1/reading/assets"},
		{[]string{"asset", "get"}, `{"type":"character","owner":"owner","slug":"dragon"}`, "/api/v1/reading/assets/character/owner/dragon"},
		{[]string{"asset", "package"}, `{"type":"worldbook","owner":"owner","slug":"book","group":"entries"}`, "/api/v1/reading/assets/worldbook/owner/book/entries"},
		{[]string{"asset", "revisions"}, `{"type":"character","owner":"owner","slug":"dragon"}`, "/api/v1/reading/assets/character/owner/dragon/history"},
		{[]string{"asset", "diff"}, `{"type":"character","owner":"owner","slug":"dragon","to":"12345678-1234-4234-8234-123456789abc"}`, "/api/v1/reading/assets/character/owner/dragon/difference"},
		{[]string{"character", "list"}, `{}`, "/api/v1/reading/catalog/characters"},
		{[]string{"resource", "use-access"}, `{"type":"skill","id":"12345678-1234-4234-8234-123456789abc"}`, "/api/v1/reading/catalog/skills/12345678-1234-4234-8234-123456789abc/use-access"},
		{[]string{"resource", "edit-access"}, `{"type":"skill","id":"12345678-1234-4234-8234-123456789abc"}`, "/api/v1/reading/catalog/skills/12345678-1234-4234-8234-123456789abc/edit-access"},
		{[]string{"worldbook", "entries", "get"}, `{"bookId":"12345678-1234-4234-8234-123456789abc","entryId":"22345678-1234-4234-8234-123456789abc"}`, "/api/v1/reading/catalog/worldbooks/12345678-1234-4234-8234-123456789abc/entries/22345678-1234-4234-8234-123456789abc"},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), append(item.args, "--input", "-"), strings.NewReader(item.input), &stdout, &stderr)
		if code != 0 || lastPath != item.path {
			t.Fatal(item.args, code, lastPath, stdout.String(), stderr.String())
		}
	}
}
