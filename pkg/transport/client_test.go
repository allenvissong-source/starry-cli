package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransportCreatedMetadataAndBoundaries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/created":
			w.Header().Set("ETag", `"version-2"`)
			w.Header().Set("Location", "/api/v1/created/id")
			w.WriteHeader(201)
			w.Write([]byte(`{"success":true,"data":{"id":"created"}}`))
		case "/api/v1/redirect":
			http.Redirect(w, r, "/api/v1/created", 302)
		case "/api/v1/large":
			w.Write([]byte(strings.Repeat("x", (4<<20)+1)))
		case "/api/v1/conflict":
			w.WriteHeader(409)
			w.Write([]byte(`{"success":false,"error":{"code":"content_changed","message":"Read again","retryable":false}}`))
		default:
			t.Error("unexpected upstream request", r.URL.Path)
		}
	}))
	defer server.Close()
	client, _ := NewClient(Config{BackendURL: server.URL})
	ctx := context.Background()
	result, err := client.Do(ctx, "POST", "/api/v1/created", "token", "intent", map[string]string{"body": "text"})
	if err != nil || result.Status != 201 || result.Header.Get("ETag") != `"version-2"` ||
		result.Header.Get("Location") != "/api/v1/created/id" || string(result.Data) != `{"id":"created"}` {
		t.Fatal(result, err)
	}
	for _, test := range []struct{ path, code string }{
		{"/api/v1/large", "response_too_large"},
		{"/api/v1/conflict", "content_changed"},
		{"/api/v1/redirect", "upstream_unavailable"},
	} {
		_, err := client.Request(ctx, "GET", test.path, "token", nil)
		var api *APIError
		if !errors.As(err, &api) || api.Code != test.code {
			t.Fatal(test.path, err)
		}
	}
	for _, path := range []string{"/api/v1/../secret", "/api/v1/x#fragment", "//elsewhere/api/v1/x"} {
		if _, err := client.Request(ctx, "GET", path, "token", nil); err == nil {
			t.Fatal("invalid path accepted", path)
		}
	}
	if _, err := client.Request(ctx, "POST", "/api/v1/created", "token", strings.Repeat("x", 64<<10)); err == nil {
		t.Fatal("oversized request accepted")
	}
}
