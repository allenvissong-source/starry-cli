package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"starry-cli/pkg/community"
)

const stateLimit = 16 << 20

type localTask struct {
	ID              string   `json:"id"`
	NotificationIDs []string `json:"notificationIds"`
	State           string   `json:"state"`
	Reason          string   `json:"reason"`
	UpdatedAt       string   `json:"updatedAt"`
}

type localState struct {
	Version int         `json:"version"`
	Binding string      `json:"binding"`
	Tasks   []localTask `json:"tasks"`
}

type stateStore struct {
	path  string
	lock  *os.File
	state localState
}

func privateStatePath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("stateFile must be a clean absolute path")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || (runtime.GOOS != "windows" && parent.Mode().Perm()&0077 != 0) {
		return errors.New("state parent must be an existing private directory (0700 on Unix)")
	}
	for _, target := range []string{path, path + ".lock"} {
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
			return errors.New("state and lock must be private regular files")
		}
	}
	return nil
}

func openState(path, binding string) (*stateStore, error) {
	if err := privateStatePath(path); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("cannot open state lock")
	}
	if err := lockState(lock); err != nil {
		lock.Close()
		return nil, errors.New("state_in_use; another process holds the local state")
	}
	store := &stateStore{path: path, lock: lock, state: localState{Version: 1, Binding: binding, Tasks: []localTask{}}}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if binding == "" {
			store.close()
			return nil, errors.New("state file does not exist")
		}
		if err := store.save(); err != nil {
			store.close()
			return nil, err
		}
		return store, nil
	}
	raw, err := readFile(path, stateLimit, true)
	if err == nil {
		err = community.DecodeInput(raw, &store.state)
	}
	if err != nil || store.state.Version != 1 || len(store.state.Binding) != 64 ||
		strings.Trim(store.state.Binding, "0123456789abcdef") != "" || (binding != "" && binding != store.state.Binding) {
		store.close()
		return nil, errors.New("invalid state or different account, backend or handler; do not reset state to retry")
	}
	ids, taskIDs := map[string]bool{}, map[string]bool{}
	for _, task := range store.state.Tasks {
		if !strings.HasPrefix(task.ID, "task_") || taskIDs[task.ID] || len(task.NotificationIDs) == 0 || len(task.NotificationIDs) > 20 ||
			(task.State != "running" && task.State != "completed" && task.State != "failed" && task.State != "unknown") ||
			!validReason(task.Reason) {
			store.close()
			return nil, errors.New("invalid local task state")
		}
		if _, err := time.Parse(time.RFC3339Nano, task.UpdatedAt); err != nil {
			store.close()
			return nil, errors.New("invalid local task time")
		}
		taskIDs[task.ID] = true
		for _, id := range task.NotificationIDs {
			if !validNotificationID(id) || ids[id] {
				store.close()
				return nil, errors.New("invalid or duplicate notification ID in state")
			}
			ids[id] = true
		}
	}
	return store, nil
}

func (s *stateStore) close() {
	unlockState(s.lock)
	s.lock.Close()
}

func (s *stateStore) save() error {
	raw, err := json.Marshal(s.state)
	if err != nil || len(raw) > stateLimit {
		return errors.New("local state capacity exceeded; dispatch stopped without dropping records")
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".starry-state-*")
	if err != nil {
		return errors.New("cannot create local state")
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.New("cannot persist local state")
	}
	if err := os.Rename(file.Name(), s.path); err != nil {
		return errors.New("cannot replace local state")
	}
	if runtime.GOOS != "windows" {
		dir, err := os.Open(filepath.Dir(s.path))
		if err != nil {
			return errors.New("cannot sync state directory")
		}
		err = dir.Sync()
		dir.Close()
		if err != nil {
			return errors.New("cannot sync state directory")
		}
	}
	return nil
}

func (s *stateStore) seen() (map[string]bool, error) {
	seen := map[string]bool{}
	for _, task := range s.state.Tasks {
		if task.State == "running" || task.State == "unknown" {
			return nil, errors.New("task_outcome_unknown; inspect watch-state and explicitly resolve before dispatch")
		}
		for _, id := range task.NotificationIDs {
			seen[id] = true
		}
	}
	return seen, nil
}

func stateBinding(backend string, principal community.Principal, cfg *handlerConfig) string {
	raw, _ := json.Marshal([]any{strings.TrimRight(backend, "/"), principal.OwnerUserID, principal.IntegrationID, cfg.Executable, cfg.Args})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type taskReceipt struct {
	TaskID string `json:"taskId"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

func validReason(reason string) bool {
	switch reason {
	case "", "no_action", "permission_denied", "insufficient_context", "host_failed", "host_timeout", "invalid_receipt", "outcome_unknown", "interrupted":
		return true
	}
	return false
}

func validNotificationID(id string) bool {
	n, err := strconv.ParseInt(id, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == id
}

func (s *stateStore) resolve(receipt taskReceipt) error {
	if (receipt.State != "completed" && receipt.State != "failed") || !validReason(receipt.Reason) {
		return errors.New("resolution requires completed or failed and a supported reason")
	}
	for i := range s.state.Tasks {
		task := &s.state.Tasks[i]
		if task.ID != receipt.TaskID {
			continue
		}
		if task.State == "completed" || task.State == "failed" {
			if task.State == receipt.State && task.Reason == receipt.Reason {
				return nil
			}
			return errors.New("cannot overwrite a completed task")
		}
		task.State, task.Reason, task.UpdatedAt = receipt.State, receipt.Reason, time.Now().UTC().Format(time.RFC3339Nano)
		return s.save()
	}
	return errors.New("local task not found")
}
