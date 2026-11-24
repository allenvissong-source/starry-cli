package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/allenvissong-source/starry-cli/pkg/community"
)

type handlerConfig struct {
	Executable     string   `json:"executable"`
	Args           []string `json:"args"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
	BatchLimit     int      `json:"batchLimit"`
	StateFile      string   `json:"stateFile"`
}

func validateHandler(cfg *handlerConfig, key string) error {
	if cfg == nil || !filepath.IsAbs(cfg.Executable) || cfg.TimeoutSeconds < 1 || cfg.TimeoutSeconds > 300 ||
		cfg.BatchLimit < 1 || cfg.BatchLimit > 20 || len(cfg.Args) > 32 {
		return errors.New("dispatch requires an absolute handler executable, timeoutSeconds 1-300 and batchLimit 1-20 in private config")
	}
	info, err := os.Stat(cfg.Executable)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("handler executable must be an existing regular file")
	}
	for _, arg := range append([]string{cfg.Executable, cfg.StateFile}, cfg.Args...) {
		if len(arg) > 4096 || (key != "" && strings.Contains(arg, key)) || strings.ContainsAny(arg, "\x00\r\n") {
			return errors.New("invalid handler arguments or state path")
		}
	}
	return privateStatePath(cfg.StateFile)
}

// This envelope carries current notification evidence, never credentials or
// permission to publish. Hosts use their own tools under existing authorization.
type hostTask struct {
	Type           string              `json:"type"`
	Mode           string              `json:"mode"`
	Task           localTask           `json:"task"`
	Items          []json.RawMessage   `json:"items"`
	Principal      community.Principal `json:"principal"`
	AllowedActions []string            `json:"allowedActions"`
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 64<<10 {
		return 0, errors.New("host output exceeds 64 KiB")
	}
	return b.Buffer.Write(p)
}

func hostEnvironment() []string {
	env := []string{}
	for _, name := range []string{"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "TEMP", "TMP", "TMPDIR", "LANG"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

func runHandler(ctx context.Context, cfg *handlerConfig, task hostTask) (taskReceipt, error) {
	data, err := json.Marshal(task)
	if err != nil {
		return taskReceipt{}, errors.New("cannot encode host task")
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, cfg.Executable, cfg.Args...)
	cmd.Env, cmd.Stdin = hostEnvironment(), bytes.NewReader(data)
	var output boundedOutput
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			return taskReceipt{}, errors.New("host_timeout")
		}
		return taskReceipt{}, errors.New("host_failed")
	}
	var receipt taskReceipt
	if community.DecodeInput(output.Bytes(), &receipt) != nil || receipt.TaskID != task.Task.ID ||
		(receipt.State != "completed" && receipt.State != "failed" && receipt.State != "unknown") || !validReason(receipt.Reason) {
		return taskReceipt{}, errors.New("invalid_receipt")
	}
	return receipt, nil
}

func sameGrant(a, b community.Principal) bool {
	return a.OwnerUserID == b.OwnerUserID && a.IntegrationID == b.IntegrationID && a.CredentialID == b.CredentialID &&
		a.GrantVersion == b.GrantVersion && slices.Equal(a.Scopes, b.Scopes) && slices.Equal(a.AllowedAIIdentityIDs, b.AllowedAIIdentityIDs)
}

type notificationPage struct {
	Items []json.RawMessage `json:"items"`
	Page  struct {
		HasMore    bool   `json:"hasMore"`
		NextCursor string `json:"nextCursor"`
		EndReason  string `json:"endReason"`
	} `json:"page"`
}

func readNotificationPage(ctx context.Context, client *community.Client, key string, principal community.Principal, cfg *handlerConfig, cursor string) (notificationPage, error) {
	return readAuthorizedPage(ctx, client, func(ctx context.Context) (community.ExchangeResult, error) {
		return client.Exchange(ctx, key)
	}, principal, cfg, cursor)
}

func readAuthorizedPage(ctx context.Context, client *community.Client, access accessProvider, principal community.Principal, cfg *handlerConfig, cursor string) (notificationPage, error) {
	var page notificationPage
	auth, err := access(ctx)
	if err != nil {
		return page, err
	}
	if !sameGrant(auth.Principal, principal) || !slices.Contains(auth.Principal.Scopes, "community.read") {
		return page, errors.New("permission_denied")
	}
	raw, _ := json.Marshal(community.NotificationsInput{Limit: &cfg.BatchLimit, Cursor: cursor})
	data, err := client.Call(ctx, auth.Token, "community_list_notifications", raw)
	if err != nil {
		return page, err
	}
	body, ok := data["data"].(json.RawMessage)
	if !ok || json.Unmarshal(body, &page) != nil || len(page.Items) > cfg.BatchLimit ||
		(page.Page.HasMore && page.Page.NextCursor == "") {
		return page, errors.New("invalid notification page")
	}
	return page, nil
}

func unseenItems(page notificationPage, seen map[string]bool) ([]string, []json.RawMessage, error) {
	ids, items := []string{}, []json.RawMessage{}
	inPage := map[string]bool{}
	for _, item := range page.Items {
		var row struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(item, &row) != nil || !validNotificationID(row.ID) || inPage[row.ID] {
			return nil, nil, errors.New("invalid notification ID")
		}
		inPage[row.ID] = true
		if !seen[row.ID] {
			ids, items = append(ids, row.ID), append(items, item)
		}
	}
	return ids, items, nil
}

func dispatchNotifications(ctx context.Context, client *community.Client, key string, principal community.Principal, cfg *handlerConfig, store *stateStore, stdout io.Writer) error {
	return dispatchAuthorized(ctx, client, func(ctx context.Context) (community.ExchangeResult, error) {
		return client.Exchange(ctx, key)
	}, principal, cfg, store, stdout)
}

func dispatchAuthorized(ctx context.Context, client *community.Client, access accessProvider, principal community.Principal, cfg *handlerConfig, store *stateStore, stdout io.Writer) error {
	seen, err := store.seen()
	if err != nil {
		return err
	}
	cursor, processed := "", 0
	cursors := map[string]bool{}
	for scanned := 0; scanned < 100; scanned++ {
		page, err := readAuthorizedPage(ctx, client, access, principal, cfg, cursor)
		if err != nil {
			return err
		}
		ids, _, err := unseenItems(page, seen)
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			if processed >= 5 {
				return errors.New("task_budget_exhausted; restart an authorized run to continue")
			}
			// Re-read this page and grant immediately before host execution. Only
			// targets still present in this response may enter the host envelope.
			fresh, err := readAuthorizedPage(ctx, client, access, principal, cfg, cursor)
			if err != nil {
				return err
			}
			freshIDs, freshItems, err := unseenItems(fresh, seen)
			if err != nil {
				return err
			}
			selected := map[string]bool{}
			for _, id := range ids {
				selected[id] = true
			}
			ids, items := []string{}, []json.RawMessage{}
			for i, id := range freshIDs {
				if selected[id] {
					ids, items = append(ids, id), append(items, freshItems[i])
				}
			}
			if len(ids) > 0 {
				random := make([]byte, 24)
				if _, err := rand.Read(random); err != nil {
					return errors.New("cannot generate local task ID")
				}
				task := localTask{ID: "task_" + hex.EncodeToString(random), NotificationIDs: ids, State: "running", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
				store.state.Tasks = append(store.state.Tasks, task)
				if err := store.save(); err != nil {
					return err
				}
				if err := json.NewEncoder(stdout).Encode(map[string]any{"type": "task_started", "task": task}); err != nil {
					return err
				}
				receipt, hostErr := runHandler(ctx, cfg, hostTask{Type: "starry_notifications", Mode: "notify", Task: task, Items: items, Principal: principal, AllowedActions: []string{}})
				if hostErr != nil {
					receipt = taskReceipt{TaskID: task.ID, State: "unknown", Reason: hostErr.Error()}
					if ctx.Err() != nil {
						receipt.Reason = "interrupted"
					}
				}
				saved := &store.state.Tasks[len(store.state.Tasks)-1]
				saved.State, saved.Reason, saved.UpdatedAt = receipt.State, receipt.Reason, time.Now().UTC().Format(time.RFC3339Nano)
				if err := store.save(); err != nil {
					return err
				}
				if err := json.NewEncoder(stdout).Encode(map[string]any{"type": "task_completed", "task": saved}); err != nil {
					return err
				}
				if hostErr != nil {
					return hostErr
				}
				if receipt.State == "unknown" {
					return errors.New("task_outcome_unknown")
				}
				for _, id := range ids {
					seen[id] = true
				}
				processed++
			}
		}
		if !page.Page.HasMore {
			if page.Page.EndReason == "scan_budget_exhausted" {
				return errors.New("notification_scan_budget_exhausted")
			}
			return nil
		}
		if cursors[page.Page.NextCursor] {
			return errors.New("repeated notification cursor")
		}
		cursor = page.Page.NextCursor
		cursors[cursor] = true
	}
	return errors.New("notification_scan_budget_exhausted; local records retained; inbox cursor not advanced")
}
