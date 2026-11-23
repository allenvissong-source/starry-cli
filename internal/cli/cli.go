package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"starry-cli/pkg/assets"
	"starry-cli/pkg/catalog"
	"starry-cli/pkg/community"
)

var commands = map[string]string{
	"identity": "account_get_identity", "list": "community_list_posts",
	"search": "community_search_posts", "topics": "community_list_topics",
	"post": "community_get_post", "comments": "community_list_comments",
	"stats": "community_get_my_stats", "notifications": "community_list_notifications",
	"inbox": "community_get_inbox", "create-dynamic": "community_create_dynamic",
	"create-article": "community_create_article", "create-comment": "community_create_comment",
	"operation": "community_get_operation",
}

const help = `Starry CLI — 直接连接后端，无需 MCP 服务
用法: starry-cli COMMAND [--input FILE|-] [--config FILE]
命令: login logout whoami identity [list|create|update]
      list search topics post comments stats notifications inbox
      create-dynamic create-article create-comment operation
      asset search|get|package|revisions|lineage|diff
      character list|get|create|update|delete|bindings|bind|unbind|media set
      worldbook list|get|create|update|delete|entries list|get|create|update|delete|apply|media set
      skill list|get|create|update|delete
      resource use-access — 每次使用前核验来源与依赖权限
      resource edit-access — 保存或另存前核验当前编辑许可
      character skills list|bind|update|unbind|apply
      collection list|get|create|update|delete; collection items list|apply
      asset operation
      asset preview|publish|policy
      asset acquire-plan|acquire|fork|stats|notifications|inbox
      asset suggestions list|get|create|withdraw|reject|adopt
      asset comments list|create
      media upload|download|operation
      character import|export; worldbook import|export; skill import|export; collection import|export
      watch [--domain community|assets] [--cursor-file FILE] [--once] [--timeout DURATION] [--dispatch]
      watch-state --state-file FILE [--input FILE|-]
配置: STARRY_BACKEND_URL, STARRY_API_KEY, STARRY_PUBLIC_WEB_URL
      或 STARRY_CLI_CONFIG 指向 0600 JSON 配置；默认用户配置目录/starry-cli/config.json
输入: 一个 JSON 对象，最多 64 KiB；Skill 与绑定写入最多 4 MiB；省略 --input 时使用 {}
login --input FILE|-: 提前提交 {name, avatar?, description?} AI 草稿，在授权页确认与编辑
输出: JSON；watch 输出 NDJSON，收到 inbox_updated 后刷新 notifications
发布必须明确 publishAs 和 requestKey；未知结果保留原 key 和完整 payload。
`

type config struct {
	BackendURL       string         `json:"backendUrl"`
	APIKey           string         `json:"apiKey"`
	PublicWebURL     string         `json:"publicWebUrl"`
	Handler          *handlerConfig `json:"handler,omitempty"`
	OAuthIssuer      string         `json:"oauthIssuer,omitempty"`
	Resource         string         `json:"resource,omitempty"`
	AccessToken      string         `json:"accessToken,omitempty"`
	AccessExpiry     time.Time      `json:"accessExpiresAt,omitempty"`
	RefreshUncertain bool           `json:"refreshUncertain,omitempty"`
}

func readBounded(reader io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, max+1))
	if err != nil || int64(len(data)) > max {
		return nil, errors.New("input exceeds limit or cannot be read")
	}
	return data, nil
}

func readFile(path string, max int64, private bool) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open input file")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("input must be a regular file")
	}
	if private && runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("credential config requires mode 0600")
	}
	return readBounded(file, max)
}

func readConfig(path string) (config, error) {
	var cfg config
	if path != "" {
		raw, err := readFile(path, 16<<10, true)
		if err != nil {
			return cfg, err
		}
		if err := community.DecodeInput(raw, &cfg); err != nil {
			return cfg, errors.New("invalid credential config")
		}
	}
	return cfg, nil
}

func loadConfig(path string) (config, error) {
	cfg, err := readConfig(path)
	if err != nil {
		return cfg, err
	}
	if value := os.Getenv("STARRY_BACKEND_URL"); value != "" {
		cfg.BackendURL = value
	}
	if value := os.Getenv("STARRY_API_KEY"); value != "" {
		cfg.APIKey = value
	}
	if value := os.Getenv("STARRY_PUBLIC_WEB_URL"); value != "" {
		cfg.PublicWebURL = value
	}
	if !validCredential(cfg.APIKey) {
		return cfg, errors.New("run login or set a manual credential in private config")
	}
	if strings.HasPrefix(cfg.APIKey, "stro_") && (os.Getenv("STARRY_API_KEY") != "" || path == "") {
		return cfg, errors.New("OAuth refresh requires a writable private config; unset STARRY_API_KEY")
	}
	return cfg, nil
}

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" || args[0] == "-h" {
		fmt.Fprint(stdout, help)
		return 0
	}
	command := args[0]
	resourceName := ""
	if command == "asset" || command == "character" || command == "worldbook" || command == "skill" || command == "collection" || command == "media" || command == "resource" {
		parts := []string{command}
		index := 1
		for index < len(args) && !strings.HasPrefix(args[index], "-") {
			parts = append(parts, args[index])
			index++
		}
		resourceName = map[string]string{
			"asset search": "assets_search", "asset get": "assets_get_revision",
			"asset package": "assets_get_package", "asset revisions": "assets_list_revisions", "asset diff": "assets_diff",
			"asset lineage":  "assets_list_lineage",
			"character list": "catalog_list_characters", "character get": "catalog_get_character",
			"character bindings":    "catalog_list_character_bindings",
			"character skills list": "catalog_list_character_skills",
			"character skills bind": "catalog_create_skill_binding", "character skills update": "catalog_update_skill_binding",
			"character skills unbind": "catalog_delete_skill_binding", "character skills apply": "catalog_apply_character_skills",
			"skill list": "catalog_list_skills", "skill get": "catalog_get_skill",
			"resource use-access":  "catalog_check_use_access",
			"resource edit-access": "catalog_check_edit_access",
			"skill create":         "catalog_create_skill", "skill update": "catalog_update_skill", "skill delete": "catalog_delete_skill",
			"collection list": "catalog_list_collections", "collection get": "catalog_get_collection",
			"collection create": "catalog_create_collection", "collection update": "catalog_update_collection", "collection delete": "catalog_delete_collection",
			"collection items list": "catalog_list_collection_items", "collection items apply": "catalog_apply_collection_items",
			"worldbook list": "catalog_list_worldbooks", "worldbook get": "catalog_get_worldbook",
			"worldbook entries list": "catalog_list_worldbook_entries", "worldbook entries get": "catalog_get_worldbook_entry",
			"character create": "catalog_create_character", "character update": "catalog_update_character", "character delete": "catalog_delete_character",
			"worldbook create": "catalog_create_worldbook", "worldbook update": "catalog_update_worldbook", "worldbook delete": "catalog_delete_worldbook",
			"worldbook entries create": "catalog_create_worldbook_entry", "worldbook entries update": "catalog_update_worldbook_entry",
			"worldbook entries delete": "catalog_delete_worldbook_entry", "worldbook entries apply": "catalog_apply_worldbook_entry_changes",
			"character bind": "catalog_bind_worldbook", "character unbind": "catalog_unbind_worldbook",
			"character media set": "catalog_set_character_media", "worldbook media set": "catalog_set_worldbook_media",
			"asset operation": "assets_get_operation",
			"asset fork":      "assets_fork", "asset stats": "assets_get_my_stats",
			"asset notifications": "assets_list_notifications", "asset inbox": "assets_get_inbox",
			"asset suggestions list": "assets_list_suggestions", "asset suggestions get": "assets_get_suggestion",
			"asset suggestions create": "assets_create_suggestion", "asset suggestions withdraw": "assets_withdraw_suggestion",
			"asset suggestions reject": "assets_reject_suggestion", "asset suggestions adopt": "assets_adopt_suggestion",
			"asset comments list": "assets_list_comments", "asset comments create": "assets_create_comment",
			"asset preview": "assets_preview_publish", "asset publish": "assets_publish", "asset policy": "assets_update_policy",
			"asset acquire":       "assets_acquire",
			"asset acquire-plan":  "assets_plan_acquisition",
			"asset update-source": "assets_get_skill_update_source",
			"media upload":        "local_media_upload", "media download": "local_media_download",
			"media operation":  "assets_get_upload",
			"character import": "local_character_import", "character export": "local_character_export",
			"worldbook import": "local_worldbook_import", "worldbook export": "local_worldbook_export",
			"skill import": "local_skill_import", "skill export": "local_skill_export",
			"collection import": "local_collection_import", "collection export": "local_collection_export",
		}[strings.Join(parts, " ")]
		if resourceName == "" {
			return report(stderr, errors.New("unknown resource action; use --help"))
		}
		args = append([]string{command}, args[index:]...)
	}
	identityAction := ""
	if command == "identity" && len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		identityAction = args[1]
		args = append([]string{command}, args[2:]...)
	}
	name, ok := commands[command]
	if resourceName != "" {
		name, ok = resourceName, true
	}
	if identityAction != "" {
		name, ok = map[string]string{"list": "ai_identity_list", "create": "ai_identity_create", "update": "ai_identity_update"}[identityAction]
		if !ok {
			return report(stderr, errors.New("unknown identity action"))
		}
	}
	if !ok && command != "watch" && command != "watch-state" && command != "login" && command != "logout" && command != "whoami" {
		return report(stderr, errors.New("unknown command; use --help"))
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	input := flags.String("input", "", "JSON file or -")
	configPath := flags.String("config", os.Getenv("STARRY_CLI_CONFIG"), "private config")
	cursorPath := flags.String("cursor-file", "", "persistent inbox revision")
	once := flags.Bool("once", false, "stop after one update")
	timeout := flags.Duration("timeout", 0, "watch duration")
	dispatch := flags.Bool("dispatch", false, "dispatch notifications to configured host")
	domain := flags.String("domain", "", "community or assets inbox")
	statePath := flags.String("state-file", "", "private local dispatch state")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return report(stderr, errors.New("invalid flags; credentials cannot be passed as arguments"))
	}
	if *domain != "" && command != "watch" || *domain != "" && *domain != "community" && *domain != "assets" || *domain == "assets" && *dispatch {
		return report(stderr, errors.New("invalid watch domain; assets supports NDJSON invalidations only"))
	}
	if *timeout < 0 || (command != "watch" && (*cursorPath != "" || *once || *dispatch)) ||
		(command != "watch" && command != "login" && *timeout != 0) ||
		((command == "logout" || command == "whoami") && *input != "") ||
		(command == "watch" && *input != "") || (command != "watch-state" && *statePath != "") {
		return report(stderr, errors.New("invalid command options"))
	}
	if command == "watch-state" {
		store, err := openState(*statePath, "")
		if err != nil {
			return report(stderr, err)
		}
		defer store.close()
		if *input != "" {
			var raw []byte
			if *input == "-" {
				raw, err = readBounded(stdin, 64<<10)
			} else {
				raw, err = readFile(*input, 64<<10, false)
			}
			var receipt taskReceipt
			if err != nil || community.DecodeInput(raw, &receipt) != nil {
				return report(stderr, errors.New("invalid local resolution"))
			}
			if err := store.resolve(receipt); err != nil {
				return report(stderr, err)
			}
		}
		if err := json.NewEncoder(stdout).Encode(map[string]any{"data": store.state}); err != nil {
			return report(stderr, errors.New("cannot write state"))
		}
		return 0
	}
	resolvedPath, err := resolveConfigPath(*configPath, command == "login")
	if err != nil {
		return report(stderr, err)
	}
	if command == "login" {
		var proposal *community.AIIdentityProposal
		if *input != "" {
			var raw []byte
			if *input == "-" {
				raw, err = readBounded(stdin, 8<<10)
			} else {
				raw, err = readFile(*input, 8<<10, false)
			}
			proposal = &community.AIIdentityProposal{}
			if err != nil || community.DecodeInput(raw, proposal) != nil {
				return report(stderr, errors.New("invalid proposed AI profile"))
			}
			if _, err := proposal.AuthorizationValue(); err != nil {
				return report(stderr, err)
			}
		}
		return runLogin(ctx, resolvedPath, *timeout, stdout, stderr, proposal)
	}
	cfg, err := loadConfig(resolvedPath)
	if err != nil {
		return report(stderr, err)
	}
	client, err := community.NewClient(community.Config{BackendURL: cfg.BackendURL, PublicWebURL: cfg.PublicWebURL})
	if err != nil {
		return report(stderr, err)
	}
	authProvider := &credentialProvider{client: client, path: resolvedPath, cfg: cfg}
	if command == "logout" {
		if err := authProvider.logout(ctx); err != nil {
			return report(stderr, err)
		}
		json.NewEncoder(stdout).Encode(map[string]any{"loggedOut": true})
		return 0
	}
	if command == "watch" {
		if *dispatch {
			if err := validateHandler(cfg.Handler, cfg.APIKey); err != nil {
				return report(stderr, err)
			}
		}
		if *timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, *timeout)
			defer cancel()
		}
		var handler *handlerConfig
		if *dispatch {
			handler = cfg.Handler
		}
		watchDomain := *domain
		if watchDomain == "" {
			watchDomain = "community"
		}
		err = watchAuthorizedDomain(ctx, client, authProvider.access, watchDomain, *cursorPath, *once, stdout, stderr, handler)
		if ctx.Err() != nil && (!*dispatch || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return 0
		}
		if err != nil {
			return report(stderr, err)
		}
		return 0
	}
	raw := json.RawMessage(`{}`)
	inputLimit := int64(catalog.InputLimit(name))
	if *input == "-" {
		raw, err = readBounded(stdin, inputLimit)
	} else if *input != "" {
		raw, err = readFile(*input, inputLimit, false)
	}
	if err != nil {
		return report(stderr, err)
	}
	requestTimeout := 30 * time.Second
	if strings.HasPrefix(name, "local_") {
		requestTimeout = 30 * time.Minute
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	auth, err := authProvider.access(requestCtx)
	if err != nil {
		return report(stderr, err)
	}
	if command == "whoami" {
		if err := json.NewEncoder(stdout).Encode(map[string]any{"data": auth.Principal}); err != nil {
			return report(stderr, errors.New("cannot write output"))
		}
		return 0
	}
	var out map[string]any
	switch name {
	case "local_character_import", "local_worldbook_import", "local_skill_import", "local_collection_import",
		"local_character_export", "local_worldbook_export", "local_skill_export", "local_collection_export":
		var input fileInput
		if err = community.DecodeInput(raw, &input); err == nil {
			kind := strings.Split(name, "_")[1]
			if strings.HasSuffix(name, "_import") {
				binding := client.BackendURL() + "\x00" + auth.Principal.OwnerUserID + "\x00" + auth.Principal.IntegrationID
				out, err = importFile(requestCtx, client.Client, auth.Token, binding, kind, input)
			} else {
				out, err = exportFile(requestCtx, client.Client, auth.Token, kind, input)
			}
		}
	case "local_media_upload":
		var input struct {
			File       string `json:"file"`
			RequestKey string `json:"requestKey"`
		}
		if err = community.DecodeInput(raw, &input); err == nil {
			out, err = assets.UploadFile(requestCtx, client.Client, auth.Token, input.File, input.RequestKey)
		}
	case "local_media_download":
		var input assets.DownloadInput
		if err = community.DecodeInput(raw, &input); err == nil {
			out, err = assets.Download(requestCtx, client.Client, auth.Token, input)
		}
	default:
		out, err = client.Call(requestCtx, auth.Token, name, raw)
	}
	if err != nil {
		return report(stderr, err)
	}
	if out["refreshRequired"] == true {
		if _, err := authProvider.authenticate(requestCtx, true); err == nil {
			out["refreshRequired"] = false
		} else {
			// Creation has committed. Keep its result and original intent visible.
			out["refreshError"] = community.ErrorPayload(err)
		}
	}
	if err := json.NewEncoder(stdout).Encode(out); err != nil {
		return report(stderr, errors.New("cannot write output"))
	}
	if out["state"] == "outcome_unknown" {
		return 2
	}
	return 0
}

func report(writer io.Writer, err error) int {
	json.NewEncoder(writer).Encode(community.ErrorPayload(err))
	return 1
}

func saveCursor(path, value string) error {
	if path == "" {
		return nil
	}
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return errors.New("cursor parent directory must exist")
	}
	file, err := os.CreateTemp(parent, ".starry-inbox-*")
	if err != nil {
		return errors.New("cannot create cursor file")
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(value + "\n"); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return errors.New("cannot save cursor")
	}
	return nil
}

func watch(ctx context.Context, client *community.Client, key, cursorPath string, once bool, stdout, stderr io.Writer, handler *handlerConfig) error {
	return watchAuthorized(ctx, client, func(ctx context.Context) (community.ExchangeResult, error) {
		return client.Exchange(ctx, key)
	}, cursorPath, once, stdout, stderr, handler)
}

type accessProvider func(context.Context) (community.ExchangeResult, error)

func watchAuthorized(ctx context.Context, client *community.Client, access accessProvider, cursorPath string, once bool, stdout, stderr io.Writer, handler *handlerConfig) error {
	return watchAuthorizedDomain(ctx, client, access, "community", cursorPath, once, stdout, stderr, handler)
}

func watchAuthorizedDomain(ctx context.Context, client *community.Client, access accessProvider, domain, cursorPath string, once bool, stdout, stderr io.Writer, handler *handlerConfig) error {
	cursor := ""
	if cursorPath != "" {
		if _, err := os.Stat(cursorPath); err == nil {
			data, err := readFile(cursorPath, 256, false)
			if err != nil {
				return err
			}
			cursor = strings.TrimSpace(string(data))
			if len(cursor) != 64 || strings.Trim(cursor, "0123456789abcdef") != "" {
				return errors.New("invalid inbox cursor file")
			}
		} else if !os.IsNotExist(err) {
			return errors.New("cannot read inbox cursor")
		}
	}
	var principal community.Principal
	var store *stateStore
	defer func() {
		if store != nil {
			store.close()
		}
	}()
	backoff := 2 * time.Second
	for {
		auth, err := access(ctx)
		if err == nil {
			principal = auth.Principal
			if handler != nil {
				binding := stateBinding(client.BackendURL(), principal, handler)
				if store == nil {
					store, err = openState(handler.StateFile, binding)
					if err != nil {
						return err
					}
				} else if store.state.Binding != binding {
					return errors.New("permission_denied")
				}
				if _, err := store.seen(); err != nil {
					return err
				}
			}
			err = community.WatchDomain(ctx, domain, cursor, 2*time.Second, access, client, principal, func(event community.InboxEvent) error {
				if err := json.NewEncoder(stdout).Encode(event); err != nil {
					return errors.New("cannot write event")
				}
				if handler != nil {
					if err := dispatchAuthorized(ctx, client, access, principal, handler, store, stdout); err != nil {
						return err
					}
				}
				if err := saveCursor(cursorPath, event.Cursor); err != nil {
					return err
				}
				cursor = event.Cursor
				backoff = 2 * time.Second
				if once {
					return io.EOF
				}
				return nil
			})
		}
		if once && errors.Is(err, io.EOF) {
			return nil
		}
		if ctx.Err() != nil {
			if handler != nil && store != nil {
				if _, stateErr := store.seen(); stateErr != nil {
					return stateErr
				}
			}
			return ctx.Err()
		}
		var api *community.APIError
		if !errors.As(err, &api) || !api.Retryable {
			return err
		}
		json.NewEncoder(stderr).Encode(community.ErrorPayload(err))
		delay := backoff
		if api.RetryAfterSeconds != nil && *api.RetryAfterSeconds > 0 {
			delay = time.Duration(*api.RetryAfterSeconds) * time.Second
		}
		if delay > time.Minute {
			delay = time.Minute
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}
