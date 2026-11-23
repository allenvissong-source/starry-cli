package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCLIFullSkillFilesExceedOrdinaryJSONLimit(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/integration-token" {
			w.Write([]byte(`{"success":true,"data":{"token":"delegate","principal":{"ownerUserId":"1000"}}}`))
			return
		}
		writes++
		if r.URL.Path != "/api/v1/authoring/catalog/skill.update" || r.ContentLength <= 64<<10 {
			t.Error("wrong large Skill request")
		}
		w.Write([]byte(`{"success":true,"data":{"result":{"state":"saved"}}}`))
	}))
	defer server.Close()
	t.Setenv("STARRY_BACKEND_URL", server.URL)
	t.Setenv("STARRY_API_KEY", "stai_test")
	t.Setenv("STARRY_CLI_CONFIG", "")
	input := `{"requestKey":"large-skill","fields":{"files":[{"path":"references/long.md","media_type":"text/markdown","content":"` + strings.Repeat("YWJj", 32<<10) + `"}]}}`
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"skill", "update", "--input", "-"}, strings.NewReader(input), &stdout, &stderr); code != 0 || writes != 1 {
		t.Fatal(code, stdout.String(), stderr.String(), writes)
	}
}

func TestResourceCommandsUseSharedHTTPReading(t *testing.T) {
	lastPath := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/integration-token" {
			w.Write([]byte(`{"success":true,"data":{"token":"delegate","principal":{"ownerUserId":"1000"}}}`))
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
		{[]string{"asset", "get"}, `{"type":"character","owner":"1000","slug":"dragon"}`, "/api/v1/reading/assets/character/1000/dragon"},
		{[]string{"asset", "package"}, `{"type":"worldbook","owner":"1000","slug":"book","group":"entries"}`, "/api/v1/reading/assets/worldbook/1000/book/entries"},
		{[]string{"asset", "revisions"}, `{"type":"character","owner":"1000","slug":"dragon"}`, "/api/v1/reading/assets/character/1000/dragon/history"},
		{[]string{"asset", "diff"}, `{"type":"character","owner":"1000","slug":"dragon","to":"12345678-1234-4234-8234-123456789abc"}`, "/api/v1/reading/assets/character/1000/dragon/difference"},
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
