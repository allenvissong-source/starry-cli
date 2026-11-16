package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"starry-cli/pkg/community"
)

// The test executable doubles as a real host and state-lock subprocess.
func TestHostProcess(t *testing.T) {
	mode := ""
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "host-mode=") {
			mode = strings.TrimPrefix(arg, "host-mode=")
		}
	}
	if mode == "" {
		return
	}
	if mode == "lock" {
		store, err := openState(os.Args[len(os.Args)-1], "")
		if err != nil {
			os.Exit(7)
		}
		defer store.close()
		os.Stdout.WriteString("locked\n")
		ioBuffer := make([]byte, 1)
		os.Stdin.Read(ioBuffer)
		os.Exit(0)
	}
	var task hostTask
	if json.NewDecoder(os.Stdin).Decode(&task) != nil {
		os.Exit(3)
	}
	for _, key := range []string{"STARRY_API_KEY", "STARRY_CLI_CONFIG", "PRIVATE_TEST_SECRET"} {
		if os.Getenv(key) != "" {
			os.Exit(4)
		}
	}
	if task.Mode != "notify" || len(task.AllowedActions) != 0 || len(task.Items) == 0 {
		os.Exit(5)
	}
	switch mode {
	case "timeout":
		<-time.After(time.Minute)
	case "invalid":
		os.Stdout.WriteString(`{"taskId":"wrong","state":"completed"}`)
	case "extra":
		os.Stdout.WriteString(`{"taskId":"` + task.Task.ID + `","state":"completed","operations":[]}`)
	case "overflow":
		os.Stdout.WriteString(strings.Repeat("x", 65537))
	case "unknown":
		json.NewEncoder(os.Stdout).Encode(taskReceipt{TaskID: task.Task.ID, State: "unknown", Reason: "outcome_unknown"})
	default:
		json.NewEncoder(os.Stdout).Encode(taskReceipt{TaskID: task.Task.ID, State: "completed", Reason: "no_action"})
	}
	os.Exit(0)
}

func testHandler(t *testing.T, mode string) *handlerConfig {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return &handlerConfig{Executable: executable, Args: []string{"-test.run=^TestHostProcess$", "host-mode=" + mode}, TimeoutSeconds: 10, BatchLimit: 20, StateFile: filepath.Join(dir, "state.json")}
}

func TestHostReceiptIsolationAndTimeout(t *testing.T) {
	t.Setenv("STARRY_API_KEY", "stai_secret")
	t.Setenv("STARRY_CLI_CONFIG", "/private/config")
	t.Setenv("PRIVATE_TEST_SECRET", "must-not-inherit")
	cfg := testHandler(t, "valid")
	task := hostTask{Mode: "notify", Task: localTask{ID: "task_test"}, Items: []json.RawMessage{json.RawMessage(`{"id":"9007199254740993"}`)}, AllowedActions: []string{}}
	if err := validateHandler(cfg, "stai_secret"); err != nil {
		t.Fatal(err)
	}
	if receipt, err := runHandler(context.Background(), cfg, task); err != nil || receipt.State != "completed" {
		t.Fatal(receipt, err)
	}
	for _, mode := range []string{"invalid", "extra", "overflow"} {
		cfg.Args[1] = "host-mode=" + mode
		if _, err := runHandler(context.Background(), cfg, task); err == nil {
			t.Fatal("invalid output accepted", mode)
		}
	}
	cfg.Args[1], cfg.TimeoutSeconds = "host-mode=timeout", 1
	start := time.Now()
	if _, err := runHandler(context.Background(), cfg, task); err == nil || err.Error() != "host_timeout" {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("host did not terminate")
	}
	cfg.Executable = "relative-host"
	if validateHandler(cfg, "stai_secret") == nil {
		t.Fatal("relative executable accepted")
	}
	cfg = testHandler(t, "valid")
	cfg.Args = []string{"stai_secret"}
	if validateHandler(cfg, "stai_secret") == nil {
		t.Fatal("credential in argv")
	}
}

func TestHandlerPrivateConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw, _ := json.Marshal(config{BackendURL: "https://example.com", APIKey: "stai_private", Handler: testHandler(t, "valid")})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STARRY_BACKEND_URL", "")
	t.Setenv("STARRY_API_KEY", "")
	cfg, err := loadConfig(path)
	if err != nil || cfg.Handler == nil || validateHandler(cfg.Handler, cfg.APIKey) != nil {
		t.Fatal(err)
	}
}

func TestDispatchLocalRestartPaginationUnknownAndRevocation(t *testing.T) {
	for _, mode := range []string{"valid", "invalid", "unknown", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			cfg := testHandler(t, mode)
			principal := community.Principal{OwnerUserID: "1000", IntegrationID: "connection", CredentialID: "credential", GrantVersion: 1, Scopes: []string{"community.read"}}
			exchanges, reads := 0, 0
			late := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var data any
				switch r.URL.Path {
				case "/api/v1/integration-token":
					exchanges++
					if mode == "revoked" && exchanges == 2 {
						w.WriteHeader(403)
						w.Write([]byte(`{"success":false,"error":{"code":"permission_denied"}}`))
						return
					}
					data = community.ExchangeResult{Token: "delegate", Principal: principal}
				case "/api/v1/community/notifications":
					reads++
					if r.URL.Query().Get("pending") != "" {
						t.Error("unexpected notification filter")
					}
					if r.URL.Query().Get("cursor") == "" {
						data = map[string]any{"items": []any{map[string]string{"id": "9007199254740993", "excerpt": "private-content"}, map[string]string{"id": "3"}}, "page": map[string]any{"hasMore": true, "nextCursor": "opaque_cursor"}}
					} else if r.URL.Query().Get("cursor") == "opaque_cursor" {
						items := []any{}
						if late {
							items = append(items, map[string]string{"id": "2"})
						}
						data = map[string]any{"items": items, "page": map[string]any{"hasMore": false, "endReason": "source_exhausted"}}
					} else {
						t.Error("cursor modified")
					}
				default:
					t.Error("unexpected API path", r.URL.Path)
				}
				json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
			}))
			defer server.Close()
			client, _ := community.NewClient(community.Config{BackendURL: server.URL})
			binding := stateBinding(server.URL, principal, cfg)
			store, err := openState(cfg.StateFile, binding)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			err = dispatchNotifications(context.Background(), client, "stai_test", principal, cfg, store, &out)
			if strings.Contains(out.String(), "stai_test") || strings.Contains(out.String(), "delegate") || strings.Contains(out.String(), "private-content") {
				t.Fatal("credential or content output")
			}
			if mode == "revoked" {
				if err == nil || len(store.state.Tasks) != 0 || reads != 1 {
					t.Fatal("revoked work dispatched", err, store.state)
				}
				store.close()
				return
			}
			if len(store.state.Tasks) != 1 || len(store.state.Tasks[0].NotificationIDs) != 2 || store.state.Tasks[0].NotificationIDs[0] != "9007199254740993" {
				t.Fatal("coalescing or ID corrupted", store.state)
			}
			id := store.state.Tasks[0].ID
			store.close()
			store, openErr := openState(cfg.StateFile, binding)
			if openErr != nil {
				t.Fatal(openErr)
			}
			defer store.close()
			out.Reset()
			if mode == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				late = true
				if err := dispatchNotifications(context.Background(), client, "stai_test", principal, cfg, store, &out); err != nil {
					t.Fatal(err)
				}
				if len(store.state.Tasks) != 2 || store.state.Tasks[1].NotificationIDs[0] != "2" {
					t.Fatal("late lower ID missed or old notification repeated", store.state)
				}
			} else {
				if err == nil || store.state.Tasks[0].State != "unknown" {
					t.Fatal("unknown not saved", err, store.state)
				}
				before := exchanges
				if dispatchNotifications(context.Background(), client, "stai_test", principal, cfg, store, &out) == nil || exchanges != before {
					t.Fatal("unknown task retried")
				}
				if err := store.resolve(taskReceipt{TaskID: id, State: "failed", Reason: "insufficient_context"}); err != nil {
					t.Fatal(err)
				}
				if err := dispatchNotifications(context.Background(), client, "stai_test", principal, cfg, store, &out); err != nil || out.Len() != 0 {
					t.Fatal("resolved task repeated", err, out.String())
				}
			}
		})
	}
}

func TestLocalStatePrivateBindingCorruptionResolutionAndCapacity(t *testing.T) {
	cfg := testHandler(t, "valid")
	binding := strings.Repeat("a", 64)
	store, err := openState(cfg.StateFile, binding)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openState(cfg.StateFile, binding); err == nil {
		t.Fatal("concurrent state acquired")
	}
	store.state.Tasks = []localTask{{ID: "task_test", NotificationIDs: []string{"1"}, State: "running", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	store.close()
	if other, err := openState(cfg.StateFile, strings.Repeat("b", 64)); err == nil {
		other.close()
		t.Fatal("different binding accepted")
	}
	t.Setenv("STARRY_API_KEY", "")
	t.Setenv("STARRY_BACKEND_URL", "")
	t.Setenv("STARRY_CLI_CONFIG", "")
	var out, errOut bytes.Buffer
	if Run(context.Background(), []string{"watch-state", "--state-file", cfg.StateFile}, nil, &out, &errOut) != 0 || !strings.Contains(out.String(), "running") {
		t.Fatal("state lookup requires network/auth", errOut.String())
	}
	if Run(context.Background(), []string{"watch-state", "--state-file", cfg.StateFile, "--input", "-"}, strings.NewReader(`{"taskId":"task_test","state":"completed","reason":"no_action"}`), &out, &errOut) != 0 {
		t.Fatal(errOut.String())
	}
	store, err = openState(cfg.StateFile, binding)
	if err != nil {
		t.Fatal(err)
	}
	if store.resolve(taskReceipt{TaskID: "task_test", State: "failed"}) == nil {
		t.Fatal("terminal state overwritten")
	}
	before, _ := os.ReadFile(cfg.StateFile)
	store.state.Tasks[0].Reason = strings.Repeat("x", stateLimit)
	if store.save() == nil {
		t.Fatal("capacity limit ignored")
	}
	after, _ := os.ReadFile(cfg.StateFile)
	if !bytes.Equal(before, after) {
		t.Fatal("oversized state replaced previous records")
	}
	store.close()
	info, _ := os.Stat(cfg.StateFile)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("state not private")
	}
	if err := os.WriteFile(cfg.StateFile, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	if store, err := openState(cfg.StateFile, binding); err == nil {
		store.close()
		t.Fatal("corrupt state reset")
	}
	if runtime.GOOS != "windows" {
		os.Chmod(filepath.Dir(cfg.StateFile), 0755)
		if privateStatePath(cfg.StateFile) == nil {
			t.Fatal("public state directory accepted")
		}
	}
}

func TestLocalStateProcessLockAndCrashRelease(t *testing.T) {
	cfg := testHandler(t, "valid")
	store, err := openState(cfg.StateFile, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cfg.Executable, "-test.run=^TestHostProcess$", "host-mode=lock", cfg.StateFile)
	if err := cmd.Run(); err == nil {
		t.Fatal("other process acquired lock")
	}
	store.close()
	cmd = exec.Command(cfg.Executable, "-test.run=^TestHostProcess$", "host-mode=lock", cfg.StateFile)
	stdin, _ := cmd.StdinPipe()
	defer stdin.Close()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	var line [7]byte
	if _, err := io.ReadFull(stdout, line[:]); err != nil || string(line[:]) != "locked\n" {
		t.Fatal("child did not acquire lock", string(line[:]), err)
	}
	if other, err := openState(cfg.StateFile, ""); err == nil {
		other.close()
		t.Fatal("parent acquired child lock")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	store, err = openState(cfg.StateFile, "")
	if err != nil {
		t.Fatal("crash did not release OS lock", err)
	}
	store.close()
}

type cancelAtStart struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelAtStart) Write(data []byte) (int, error) {
	n, err := w.Buffer.Write(data)
	if bytes.Contains(data, []byte(`"task_started"`)) {
		w.cancel()
	}
	return n, err
}

func TestWatchDispatchBudgetAndCancellationKeepCursor(t *testing.T) {
	for _, mode := range []string{"task-budget", "page-budget", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			cfg := testHandler(t, "valid")
			if mode == "cancelled" {
				cfg.Args[1] = "host-mode=timeout"
			}
			principal := community.Principal{OwnerUserID: "1000", IntegrationID: "c", CredentialID: "k", GrantVersion: 1, Scopes: []string{"community.read"}}
			notificationReads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var data any
				switch r.URL.Path {
				case "/api/v1/integration-token":
					data = community.ExchangeResult{Token: "delegate", Principal: principal}
				case "/api/v1/community/inbox":
					data = map[string]string{"cursor": strings.Repeat("a", 64)}
				case "/api/v1/community/notifications":
					notificationReads++
					page, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
					items := []any{map[string]string{"id": strconv.Itoa(page + 1)}}
					if mode == "page-budget" {
						items = []any{}
					}
					data = map[string]any{"items": items, "page": map[string]any{"hasMore": true, "nextCursor": strconv.Itoa(page + 1)}}
				default:
					t.Error("unexpected path", r.URL.Path)
				}
				json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
			}))
			defer server.Close()
			client, _ := community.NewClient(community.Config{BackendURL: server.URL})
			cursorPath := filepath.Join(filepath.Dir(cfg.StateFile), "cursor")
			previous := strings.Repeat("b", 64)
			if err := saveCursor(cursorPath, previous); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			var out io.Writer = &bytes.Buffer{}
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				out = &cancelAtStart{cancel: cancel}
			}
			var errOut bytes.Buffer
			err := watch(ctx, client, "stai_test", cursorPath, true, out, &errOut, cfg)
			if err == nil {
				t.Fatal("budget or cancelled host reported success")
			}
			raw, _ := os.ReadFile(cursorPath)
			if strings.TrimSpace(string(raw)) != previous {
				t.Fatal("inbox cursor advanced despite incomplete dispatch")
			}
			store, err := openState(cfg.StateFile, stateBinding(server.URL, principal, cfg))
			if err != nil {
				t.Fatal(err)
			}
			defer store.close()
			switch mode {
			case "task-budget":
				if len(store.state.Tasks) != 5 {
					t.Fatal("task budget ignored", len(store.state.Tasks))
				}
			case "page-budget":
				if notificationReads != 100 || len(store.state.Tasks) != 0 {
					t.Fatal("page budget ignored", notificationReads, store.state)
				}
			case "cancelled":
				if len(store.state.Tasks) != 1 || store.state.Tasks[0].State != "unknown" || store.state.Tasks[0].Reason != "interrupted" {
					t.Fatal("cancelled host state not retained", store.state)
				}
			}
		})
	}
}
