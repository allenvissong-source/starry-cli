package assets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"starry-cli/pkg/transport"
)

func TestMediaFileUploadAndCASReplayUsesSameHandle(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("p", 100))
	sum := sha256.Sum256(png)
	digest := hex.EncodeToString(sum[:])
	id := "12345678-1234-4234-8234-123456789abc"
	uploaded, registrations := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer delegated" {
			t.Error("lost credential")
		}
		switch {
		case r.Method == "GET":
			if uploaded == 0 {
				w.WriteHeader(404)
				io.WriteString(w, `{"success":false,"error":{"code":"cas.blob_not_found"}}`)
			} else {
				fmt.Fprintf(w, `{"success":true,"data":{"sha256":%q,"byte_size":%d}}`, digest, len(png))
			}
		case r.Method == "PUT":
			got, _ := io.ReadAll(r.Body)
			if !bytes.Equal(got, png) || r.ContentLength != int64(len(png)) || r.Header.Get("Content-Type") != "image/png" {
				t.Error("binary upload changed")
			}
			uploaded++
			w.WriteHeader(201)
			fmt.Fprintf(w, `{"success":true,"data":{"sha256":%q,"byte_size":%d}}`, digest, len(png))
		case r.Method == "POST":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["sha256"] != digest || body["client_mutation_id"] != "stable-media" {
				t.Error("registration key changed")
			}
			registrations++
			fmt.Fprintf(w, `{"success":true,"data":{"asset_id":%q,"sha256":%q}}`, id, digest)
		}
	}))
	defer server.Close()
	client, _ := transport.NewClient(transport.Config{BackendURL: server.URL})
	file := filepath.Join(t.TempDir(), "avatar.png")
	os.WriteFile(file, png, 0600)
	for range 2 {
		out, err := UploadFile(context.Background(), client, "delegated", file, "stable-media")
		if err != nil || out["data"].(map[string]any)["asset_id"] != id {
			t.Fatal(out, err)
		}
	}
	if uploaded != 1 || registrations != 2 {
		t.Fatal(uploaded, registrations)
	}
	for _, raw := range []string{
		`{"file":"/etc/passwd"}`, `{"url":"https://example.com/a.png"}`,
		`{"sha256":"bad","mediaType":"image/png","byteSize":1,"requestKey":"x"}`,
	} {
		if _, err := Media(context.Background(), client, "delegated", "assets_finalize_upload", json.RawMessage(raw)); err == nil {
			t.Fatal("accepted untrusted handle")
		}
	}
}

func TestMediaDownloadRejectsRedirectAndHashMismatchWithoutPartialOutput(t *testing.T) {
	data := []byte("media payload")
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("bearer leaked to media host")
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/bytes", 302)
			return
		}
		w.Write(data)
	}))
	defer target.Close()
	client, _ := transport.NewClient(transport.Config{BackendURL: target.URL})
	dir := t.TempDir()
	file := filepath.Join(dir, "media.bin")
	for _, input := range []struct{ path, hash string }{{"/redirect", digest}, {"/bytes", strings.Repeat("0", 64)}} {
		if DownloadURL(context.Background(), client, target.URL+input.path, input.hash, "", file) == nil {
			t.Fatal("bad download accepted")
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatal("partial output exposed")
		}
	}
	if err := DownloadURL(context.Background(), client, target.URL+"/bytes", digest, "", file); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file)
	if !bytes.Equal(got, data) {
		t.Fatal("media changed")
	}
	if err := DownloadURL(context.Background(), client, target.URL+"/bytes", digest, "", file); err == nil {
		t.Fatal("existing output overwritten")
	}
}
