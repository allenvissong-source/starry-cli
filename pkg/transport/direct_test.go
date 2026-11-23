package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDirectUploadPreservesCancellation(t *testing.T) {
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("canceled upload reached edge")
	}))
	defer edge.Close()
	client, err := NewClient(Config{BackendURL: edge.URL, CASDirectUploadOrigin: edge.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.UploadDirect(ctx, edge.URL+"/uploads/ticket", "ticket", "image/webp", 1, strings.NewReader("x"))
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDefaultUploadOriginRejectsTestAndStaticOrigins(t *testing.T) {
	client, err := NewClient(Config{BackendURL: "https://api.dustarry.com"})
	if err != nil || client.CASDirectUploadOrigin() != "https://upload.dustarry.com" {
		t.Fatal("incorrect production upload origin", err)
	}
	for _, origin := range []string{"https://r2-test.dustarry.com", "https://r2.dustarry.com"} {
		_, err := client.UploadDirect(context.Background(), origin+"/uploads/ticket", "ticket", "image/webp", 1, strings.NewReader("x"))
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Code != "invalid_direct_upload" {
			t.Fatal("upload accepted non-production origin", origin, err)
		}
	}
}
