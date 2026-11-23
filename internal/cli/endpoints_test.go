package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProductionEndpointsAndExplicitOverrides(t *testing.T) {
	for _, key := range []string{"STARRY_BACKEND_URL", "STARRY_PUBLIC_WEB_URL", "STARRY_CAS_DIRECT_UPLOAD_ORIGIN", "STARRY_API_KEY"} {
		t.Setenv(key, "")
	}
	t.Setenv("STARRY_API_KEY", "stai_endpoint_test")
	cfg, err := loadConfig("")
	if err != nil || cfg.BackendURL != "https://api.dustarry.com" ||
		cfg.PublicWebURL != "https://dustarry.com" || cfg.CASDirectUploadOrigin != "https://upload.dustarry.com" {
		t.Fatalf("fresh CLI did not resolve production endpoints: %+v %v", cfg, err)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"backendUrl":"http://127.0.0.1:18082","publicWebUrl":"http://127.0.0.1:8080","casDirectUploadOrigin":"http://127.0.0.1:8090"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig(path)
	if err != nil || cfg.BackendURL != "http://127.0.0.1:18082" ||
		cfg.PublicWebURL != "http://127.0.0.1:8080" || cfg.CASDirectUploadOrigin != "http://127.0.0.1:8090" {
		t.Fatal("explicit config endpoints lost", err)
	}
	t.Setenv("STARRY_BACKEND_URL", "https://selfhost.example")
	t.Setenv("STARRY_PUBLIC_WEB_URL", "https://web.example")
	t.Setenv("STARRY_CAS_DIRECT_UPLOAD_ORIGIN", "https://media.example")
	cfg, err = loadConfig(path)
	if err != nil || cfg.BackendURL != "https://selfhost.example" ||
		cfg.PublicWebURL != "https://web.example" || cfg.CASDirectUploadOrigin != "https://media.example" {
		t.Fatal("environment endpoints did not override file", err)
	}
}

func TestLoginEndpointsDoNotInventProductionLinksForOtherBackends(t *testing.T) {
	for _, key := range []string{"STARRY_BACKEND_URL", "STARRY_PUBLIC_WEB_URL", "STARRY_CAS_DIRECT_UPLOAD_ORIGIN"} {
		t.Setenv(key, "")
	}
	for _, backend := range []string{"https://selfhost.example", "http://127.0.0.1:18082"} {
		cfg := config{BackendURL: backend}
		configureEndpoints(&cfg)
		if cfg.BackendURL != backend || cfg.PublicWebURL != "" {
			t.Fatal("explicit backend changed or received incorrect public links")
		}
	}
	cfg := config{}
	configureEndpoints(&cfg)
	if cfg.BackendURL != "https://api.dustarry.com" || cfg.PublicWebURL != "https://dustarry.com" ||
		cfg.CASDirectUploadOrigin != "https://upload.dustarry.com" {
		t.Fatal("fresh login missing production endpoints")
	}
}
