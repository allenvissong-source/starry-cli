package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestTransportCanceledRequestClosesUpstreamWithoutRetry(t *testing.T) {
	for _, partialBody := range []bool{false, true} {
		name := "waiting_for_headers"
		if partialBody {
			name = "reading_body"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			disconnected := make(chan struct{})
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if partialBody {
					w.Header().Set("Content-Type", "application/json")
					w.Write([]byte(`{"success":true,"data":`))
					w.(http.Flusher).Flush()
				}
				close(started)
				<-r.Context().Done()
				close(disconnected)
			}))
			defer server.Close()
			client, err := NewClient(Config{BackendURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := client.Request(ctx, http.MethodGet, "/api/v1/wait", "token", nil)
				result <- err
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("upstream did not receive the request")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation became a retryable upstream error: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("client did not stop after cancellation")
			}
			select {
			case <-disconnected:
			case <-time.After(3 * time.Second):
				t.Fatal("cancellation did not reach the upstream connection")
			}
			if requests.Load() != 1 {
				t.Fatalf("canceled request retried: %d requests", requests.Load())
			}
		})
	}
}

func TestTransportExpiredContextDoesNotSend(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	client, err := NewClient(Config{BackendURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := client.Request(ctx, http.MethodGet, "/api/v1/wait", "token", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired deadline became an upstream error: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatal("expired request reached the upstream")
	}
}
