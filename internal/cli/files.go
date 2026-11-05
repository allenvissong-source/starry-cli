package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"unicode/utf8"

	"starry-cli/pkg/assets"
	"starry-cli/pkg/catalog"
	"starry-cli/pkg/transport"
)

const maxGraphBytes = 64 << 20

type fileInput struct {
	File        string `json:"file,omitempty"`
	Progress    string `json:"progress,omitempty"`
	RequestKey  string `json:"requestKey,omitempty"`
	InspectOnly bool   `json:"inspectOnly,omitempty"`
	Source      string `json:"source,omitempty"`
	ID          string `json:"id,omitempty"`
	Owner       string `json:"owner,omitempty"`
	Slug        string `json:"slug,omitempty"`
	Revision    string `json:"revision,omitempty"`
	Output      string `json:"output,omitempty"`
}

type fileGraph struct {
	Character  map[string]any   `json:"character"`
	Worldbooks []map[string]any `json:"worldbooks"`
	Bindings   []map[string]any `json:"bindings"`
	Relations  []map[string]any `json:"relations"`
	Media      []map[string]any `json:"media"`
	Warnings   []string         `json:"warnings"`
}

type importStep struct {
	Name   string             `json:"name"`
	Input  catalog.WriteInput `json:"input"`
	Status string             `json:"status"`
	Result map[string]any     `json:"result,omitempty"`
}

type importProgress struct {
	Version int               `json:"version"`
	Binding string            `json:"binding"`
	Digest  string            `json:"digest"`
	Key     string            `json:"key"`
	Type    string            `json:"type"`
	Graph   fileGraph         `json:"graph"`
	Steps   []importStep      `json:"steps"`
	Media   map[string]string `json:"media"`
}

func stableID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	sum[6], sum[8] = (sum[6]&15)|0x50, (sum[8]&63)|0x80
	s := hex.EncodeToString(sum[:16])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

func formatHost(ctx context.Context, action, kind, input, output string) error {
	executable, err := os.Executable()
	if err != nil {
		return errors.New("cannot locate format host")
	}
	host := filepath.Join(filepath.Dir(executable), "starry-format-host")
	if runtime.GOOS == "windows" {
		host += ".exe"
	}
	// The shipped host is an executable beside the CLI. A development SDK is
	// never invoked implicitly, and user input cannot select an executable.
	cmd := exec.CommandContext(ctx, host, action, kind, input, output)
	cmd.Env = []string{}
	if runtime.GOOS == "windows" {
		cmd.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot")}
	}
	if err := cmd.Run(); err != nil {
		return errors.New("format host unavailable or file cannot be represented completely")
	}
	return nil
}

func validateSource(path, kind string) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if kind == "worldbook" && ext != ".json" || kind == "character" && !slices.Contains([]string{".json", ".png", ".charx", ".starry"}, ext) {
		return nil, errors.New("unsupported format; standalone worldbooks support JSON")
	}
	raw, err := readFile(path, 128<<20, false)
	if err != nil || len(raw) == 0 {
		return nil, errors.New("source must be a bounded regular file")
	}
	if ext == ".charx" || ext == ".starry" {
		z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			return nil, errors.New("invalid archive")
		}
		if len(z.File) > 2048 {
			return nil, errors.New("archive has too many files")
		}
		names := map[string]bool{}
		var total uint64
		images := 0
		for _, f := range z.File {
			name := f.Name
			clean := filepath.ToSlash(filepath.Clean(name))
			if name == "" || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") ||
				clean == ".." || strings.HasPrefix(clean, "../") || strings.TrimSuffix(name, "/") != clean ||
				names[clean] || f.Mode()&os.ModeSymlink != 0 ||
				(!f.Mode().IsRegular() && !f.Mode().IsDir()) || f.Flags&1 != 0 {
				return nil, errors.New("unsafe or duplicate archive entry")
			}
			names[clean] = true
			if ext == ".charx" && !f.Mode().IsDir() {
				if slices.Contains([]string{".png", ".jpg", ".jpeg", ".gif", ".webp"}, strings.ToLower(filepath.Ext(name))) {
					images++
					if images > 1 {
						return nil, errors.New("CharX contains more media than this codec can represent")
					}
				}
				if name == "card.json" {
					reader, err := f.Open()
					if err != nil {
						return nil, errors.New("cannot inspect CharX card")
					}
					card, err := io.ReadAll(io.LimitReader(reader, maxGraphBytes+1))
					reader.Close()
					if err != nil || len(card) > maxGraphBytes {
						return nil, errors.New("CharX card exceeds limit")
					}
					if err := rejectForeignAssetReferences(card); err != nil {
						return nil, err
					}
				}
			}
			if f.UncompressedSize64 > 128<<20 {
				return nil, errors.New("archive entry exceeds limit")
			}
			total += f.UncompressedSize64
			if total > 256<<20 {
				return nil, errors.New("archive expansion exceeds limit")
			}
			// Verify actual expansion and CRC before the codec materializes data.
			r, err := f.Open()
			if err != nil {
				return nil, errors.New("cannot read archive entry")
			}
			n, readErr := io.Copy(io.Discard, io.LimitReader(r, int64(f.UncompressedSize64)+1))
			r.Close()
			if readErr != nil || uint64(n) != f.UncompressedSize64 {
				return nil, errors.New("archive size or checksum mismatch")
			}
		}
	}
	if ext == ".json" && kind == "character" {
		if err := rejectForeignAssetReferences(raw); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

func rejectForeignAssetReferences(raw []byte) error {
	var card map[string]any
	if json.Unmarshal(raw, &card) != nil {
		return errors.New("invalid character JSON")
	}
	source := card
	if nested := asObject(card["data"]); nested != nil {
		source = nested
	}
	if refs, ok := source["assets"].([]any); ok && len(refs) > 0 {
		return errors.New("foreign card asset references need a complete supported package")
	}
	return nil
}

func saveProgress(path string, value any) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".starry-import-*")
	if err != nil {
		return errors.New("progress parent must exist and be writable")
	}
	defer os.Remove(f.Name())
	raw, err := json.Marshal(value)
	if len(raw) > maxGraphBytes {
		f.Close()
		return errors.New("import progress exceeds 64 MiB")
	}
	if err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

var characterFileFields = strings.Fields("name nickname description personality scenario first_message example_messages system_prompt post_history_instructions creator_notes character_version alternate_greetings tags depth_prompt talkativeness")
var bookFileFields = strings.Fields("name description creator_notes recursive_scanning allow_entry_cascade scan_depth token_budget max_recursion_depth tags extensions")
var entryFileFields = strings.Fields("entry_uid comment content position group_name automation_id outlet_name vectorized_value enabled use_probability case_sensitive match_whole_words constant selective use_group use_group_scoring prevent_recursion exclude_recursion delay_until_recursion add_memo is_favorite role entry_order depth sticky cooldown delay selective_logic group_weight group_override entry_scan_depth insertion_order display_index probability keys secondary_keys extensions character_filter")

func selectFields(in map[string]any, fields []string) map[string]any {
	out := map[string]any{}
	for _, key := range fields {
		if v, ok := in[key]; ok {
			out[key] = v
		}
	}
	return out
}

func asObject(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
func text(v any) string   { s, _ := v.(string); return s }
func integer(v any) int64 { n, _ := v.(float64); return int64(n) }

func publishedMediaSlot(file map[string]any) (string, error) {
	path, role, hash := text(file["logical_path"]), text(file["reference_role"]), text(file["sha256"])
	if !assets.MediaHash.MatchString(hash) {
		return "", errors.New("published media needs a content digest")
	}
	// Delivery roles describe permissions, while authored slots are encoded
	// by the Catalog logical path. Both must agree before reconstructing a slot.
	if path == "cover_asset_id" && role == "cover" {
		return "cover", nil
	}
	for slot, delivery := range map[string]string{
		"avatar": "preview", "cover": "cover", "illustration": "file", "background": "file",
	} {
		if path == slot+"/"+hash && role == delivery {
			return slot, nil
		}
	}
	return "", errors.New("published file is not a supported authored media slot")
}

func normalizeGraph(g *fileGraph, kind, seed string) ([]importStep, error) {
	for _, warning := range g.Warnings {
		if strings.Contains(strings.ToLower(warning), "duplicate") || strings.Contains(strings.ToLower(warning), "lost") {
			return nil, errors.New("source has lossy parsing warnings; repair source before importing")
		}
	}
	steps := []importStep{}
	add := func(name, id, parent string, fields map[string]any) {
		steps = append(steps, importStep{Name: name, Status: "prepared",
			Input: catalog.WriteInput{RequestKey: stableID(seed + fmt.Sprint(len(steps))),
				ID: id, ParentID: parent, Fields: fields}})
	}
	if kind == "character" {
		if strings.TrimSpace(text(g.Character["name"])) == "" {
			return nil, errors.New("character name is required")
		}
		g.Character["id"] = stableID(seed + "character")
		add("catalog_create_character", text(g.Character["id"]), "", selectFields(g.Character, characterFileFields))
	}
	bookIDs := map[string]string{}
	bookMedia := []importStep{}
	for i, book := range g.Worldbooks {
		old := text(book["id"])
		if old == "" || bookIDs[old] != "" || strings.TrimSpace(text(book["name"])) == "" {
			return nil, errors.New("invalid or duplicate worldbook")
		}
		book["id"] = stableID(seed + fmt.Sprintf("book:%d", i))
		bookIDs[old] = text(book["id"])
		ext := asObject(book["extensions"])
		for role, rawHash := range asObject(ext["starry_media"]) {
			hash := text(rawHash)
			if hash == "" {
				continue
			}
			bookMedia = append(bookMedia, importStep{Name: "catalog_set_worldbook_media", Status: "prepared",
				Input: catalog.WriteInput{RequestKey: stableID(seed + text(book["id"]) + role),
					ID: text(book["id"]), MediaRole: role, Fields: map[string]any{"sourceHash": hash}}})
		}
		for _, key := range []string{"starry_media", "__overviewMedia", "__overviewRelation"} {
			delete(ext, key)
		}
		add("catalog_create_worldbook", text(book["id"]), "", selectFields(book, bookFileFields))
		entries, _ := book["entries"].([]any)
		if len(entries) > 10000 {
			return nil, errors.New("worldbook exceeds 10000 entries")
		}
		uids := map[string]bool{}
		for j, raw := range entries {
			entry := asObject(raw)
			for key, value := range asObject(asObject(entry["extensions"])["st_extra"]) {
				if _, exists := entry[key]; !exists {
					entry[key] = value
				}
			}
			for source, target := range map[string]string{"uid": "entry_uid", "order": "entry_order", "group": "group_name",
				"scan_depth": "entry_scan_depth", "vectorized": "vectorized_value"} {
				if v, ok := entry[source]; ok {
					entry[target] = v
				}
			}
			uid := text(entry["entry_uid"])
			if uid == "" || uids[uid] {
				return nil, errors.New("entry UID missing or duplicated")
			}
			uids[uid] = true
			extra := asObject(entry["extensions"])
			if extra == nil {
				extra = map[string]any{}
			}
			if n, ok := entry["id"].(float64); ok {
				extra["starry_file_source"] = map[string]any{"numeric_id": n}
			}
			if value := entry["vectorized_value"]; value != nil {
				if _, ok := value.(string); !ok {
					source := asObject(extra["starry_file_source"])
					if source == nil {
						source = map[string]any{}
						extra["starry_file_source"] = source
					}
					source["vectorized"] = value
					entry["vectorized_value"] = fmt.Sprint(value)
				}
			}
			if entry["outlet_name"] == nil && extra["outlet_name"] != nil {
				entry["outlet_name"] = extra["outlet_name"]
			}
			entry["extensions"] = extra
			if entry["character_filter"] == nil {
				entry["character_filter"] = map[string]any{}
			}
			if entry["vectorized_value"] == nil {
				entry["vectorized_value"] = ""
			}
			id := stableID(seed + fmt.Sprintf("book:%d:entry:%d", i, j))
			entry["id"] = id
			add("catalog_create_worldbook_entry", id, text(book["id"]), selectFields(entry, entryFileFields))
		}
	}
	seenBooks := map[string]bool{}
	for i, b := range g.Bindings {
		book := bookIDs[text(b["worldbook_id"])]
		if book == "" || seenBooks[book] || b["enabled"] == false ||
			!slices.Contains([]string{"embedded", "reference"}, text(b["reference_type"])) {
			return nil, errors.New("unresolved, disabled or duplicate binding")
		}
		seenBooks[book] = true
		b["worldbook_id"] = book
		add("catalog_bind_worldbook", stableID(seed+fmt.Sprintf("binding:%d", i)), text(g.Character["id"]),
			map[string]any{"worldbook_id": book, "reference_type": b["reference_type"], "sort_order": i})
	}
	media := map[string]bool{}
	for _, m := range g.Media {
		hash := text(m["sha256"])
		if !assets.MediaHash.MatchString(hash) || media[hash] {
			return nil, errors.New("invalid or duplicate packaged media")
		}
		media[hash] = true
	}
	roles := map[string]bool{}
	for _, r := range g.Relations {
		role, hash := text(r["role"]), text(r["target_id"])
		if text(r["target_type"]) != "asset" || !slices.Contains([]string{"avatar", "cover", "illustration", "background"}, role) ||
			!media[hash] || roles[role] || r["enabled"] == false || r["export_included"] == false ||
			len(asObject(r["settings"])) > 0 || len(asObject(r["annotations"])) > 0 ||
			(text(r["profile"]) != "" && text(r["profile"]) != "default") {
			return nil, errors.New("presentation graph cannot be represented by four media slots")
		}
		roles[role] = true
		steps = append(steps, importStep{Name: "catalog_set_character_media", Status: "prepared",
			Input: catalog.WriteInput{RequestKey: stableID(seed + "media:" + role),
				ID: text(g.Character["id"]), MediaRole: role, Fields: map[string]any{"sourceHash": hash}}})
	}
	for _, step := range bookMedia {
		if !slices.Contains([]string{"avatar", "cover", "illustration", "background"}, step.Input.MediaRole) ||
			!media[text(step.Input.Fields["sourceHash"])] {
			return nil, errors.New("unresolved worldbook media")
		}
		steps = append(steps, step)
	}
	// Reject oversized/invalid fields before the first HTTP write.
	for _, step := range steps {
		raw, _ := json.Marshal(step.Input)
		if len(raw) > 64<<10 {
			return nil, errors.New("single authoring intent exceeds 64 KiB")
		}
		for key, v := range step.Input.Fields {
			if s, ok := v.(string); ok && len(s) > 48000 || key == "name" && len(text(v)) > 255 {
				return nil, errors.New("field exceeds Catalog limit")
			}
		}
	}
	return steps, nil
}

func importFile(ctx context.Context, client *transport.Client, token, binding, kind string, in fileInput) (map[string]any, error) {
	if in.Progress == "" || in.File == "" || in.RequestKey == "" || len(in.RequestKey) > 128 ||
		in.Source != "" || in.Output != "" || in.ID != "" || in.Owner != "" || in.Slug != "" || in.Revision != "" {
		return nil, errors.New("import requires file, stable requestKey and private progress path")
	}
	lock := in.Progress + ".lock"
	if err := os.Mkdir(lock, 0700); err != nil {
		return nil, errors.New("import progress is locked or parent is unavailable")
	}
	defer os.Remove(lock)
	var progress importProgress
	stored, err := readFile(in.Progress, maxGraphBytes, true)
	if err == nil {
		if json.Unmarshal(stored, &progress) != nil || progress.Version != 1 ||
			progress.Binding != binding || progress.Key != in.RequestKey || progress.Type != kind {
			return nil, errors.New("progress belongs to another import, account or connection")
		}
	} else {
		if _, statErr := os.Lstat(in.Progress); !os.IsNotExist(statErr) {
			return nil, err
		}
		raw, err := validateSource(in.File, kind)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(raw)
		progress = importProgress{Version: 1, Binding: binding, Digest: hex.EncodeToString(sum[:]),
			Key: in.RequestKey, Type: kind, Media: map[string]string{}}
		stage := in.Progress + ".files"
		if err := os.Mkdir(stage, 0700); err != nil {
			return nil, errors.New("import staging directory already exists or is unavailable")
		}
		keepStage := false
		defer func() {
			if !keepStage {
				os.RemoveAll(stage)
			}
		}()
		// Parse this immutable copy, not the mutable source path.
		source := filepath.Join(stage, "source"+strings.ToLower(filepath.Ext(in.File)))
		if err := os.WriteFile(source, raw, 0600); err != nil {
			return nil, err
		}
		if err := formatHost(ctx, "inspect", kind, source, stage); err != nil {
			return nil, err
		}
		graph, err := readFile(filepath.Join(stage, "graph.json"), maxGraphBytes, false)
		if err != nil || json.Unmarshal(graph, &progress.Graph) != nil {
			return nil, errors.New("invalid format host graph")
		}
		progress.Steps, err = normalizeGraph(&progress.Graph, kind, binding+in.RequestKey+progress.Digest)
		if err != nil {
			return nil, err
		}
		if err := saveProgress(in.Progress, progress); err != nil {
			return nil, err
		}
		keepStage = true
	}
	if in.InspectOnly {
		return map[string]any{"data": map[string]any{"state": "inspected", "sha256": progress.Digest,
			"steps": len(progress.Steps), "worldbooks": len(progress.Graph.Worldbooks), "media": len(progress.Graph.Media),
			"warnings": progress.Graph.Warnings, "progress": in.Progress}}, nil
	}
	for _, m := range progress.Graph.Media {
		hash := text(m["sha256"])
		if progress.Media[hash] != "" {
			if progress.Media[hash] == "outcome_unknown" {
				operation, err := client.Request(ctx, "GET", "/api/v1/authoring/cas/assets?"+url.Values{
					"requestKey": {stableID(progress.Key + progress.Digest + hash)}, "sha256": {hash}}.Encode(), token, nil)
				var result map[string]any
				if err != nil || json.Unmarshal(operation, &result) != nil || !assets.UUID.MatchString(text(result["asset_id"])) ||
					result["sha256"] != hash {
					return nil, errors.New("media registration not confirmed; retain original key and source, no upload retried")
				}
				progress.Media[hash] = text(result["asset_id"])
				if err := saveProgress(in.Progress, progress); err != nil {
					return nil, err
				}
			}
			continue
		}
		progress.Media[hash] = "outcome_unknown"
		if err := saveProgress(in.Progress, progress); err != nil {
			return nil, err
		}
		result, err := assets.UploadFile(ctx, client, token, text(m["file"]), stableID(progress.Key+progress.Digest+hash))
		if err != nil {
			return nil, err
		}
		progress.Media[hash] = text(asObject(result["data"])["asset_id"])
		if err := saveProgress(in.Progress, progress); err != nil {
			return nil, err
		}
	}
	for i := range progress.Steps {
		step := &progress.Steps[i]
		if step.Status == "complete" {
			continue
		}
		if step.Status == "attempted" {
			raw, _ := json.Marshal(catalog.OperationInput{Action: catalog.WriteActions[step.Name], RequestKey: step.Input.RequestKey})
			result, err := catalog.Operation(ctx, client, token, raw)
			if err != nil {
				return nil, errors.New("operation not confirmed; preserve original progress and intent, no write retried")
			}
			step.Result, step.Status = asObject(asObject(result["data"])["result"]), "complete"
			if err := saveProgress(in.Progress, progress); err != nil {
				return nil, err
			}
			continue
		}
		if step.Input.ParentID != "" || strings.Contains(step.Name, "_media") {
			parent, parentKind := step.Input.ParentID, "worldbook"
			if step.Name == "catalog_bind_worldbook" {
				parentKind = "character"
			}
			if strings.Contains(step.Name, "_media") {
				parent, parentKind = step.Input.ID, "character"
				if step.Name == "catalog_set_worldbook_media" {
					parentKind = "worldbook"
				}
				assetID := progress.Media[text(step.Input.Fields["sourceHash"])]
				step.Input.Fields = nil
				step.Input.MediaAssetID = &assetID
			}
			content, _, err := readContent(ctx, client, token, "/api/v1/reading/catalog/"+parentKind+"s/"+parent, nil)
			if err != nil {
				return nil, err
			}
			step.Input.ExpectedReadingVersion = text(content["readingVersion"])
			if strings.Contains(step.Name, "_media") {
				step.Input.ExpectedVersion = integer(content["version"])
			}
		}
		step.Status = "attempted"
		if err := saveProgress(in.Progress, progress); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(step.Input)
		out, err := catalog.Write(ctx, client, token, step.Name, raw)
		if err != nil {
			return nil, err
		}
		step.Result, step.Status = asObject(asObject(out["data"])["result"]), "complete"
		if err := saveProgress(in.Progress, progress); err != nil {
			return nil, err
		}
	}
	id := text(progress.Graph.Character["id"])
	if kind == "worldbook" {
		id = text(progress.Graph.Worldbooks[0]["id"])
	}
	return map[string]any{"data": map[string]any{"state": "saved", "id": id, "progress": in.Progress,
		"steps": len(progress.Steps), "warnings": progress.Graph.Warnings}}, nil
}

type contentField struct {
	Path     string `json:"path"`
	Encoding string `json:"encoding"`
	Length   int    `json:"length"`
}
type filePage struct {
	Next string `json:"nextCursor"`
	More bool   `json:"hasMore"`
	End  string `json:"endReason"`
}

// Reassemble selected fields using Unicode offsets; never accept a prefix as
// valid JSON or stitch data from two different workspace versions.
func readContent(ctx context.Context, client *transport.Client, token, path string, q url.Values) (map[string]any, map[string]any, error) {
	if q == nil {
		q = url.Values{}
	}
	fields := []contentField{}
	texts := map[string]string{}
	var metadata map[string]any
	var version, revision string
	total := 0
	cursor := ""
	for page := 0; page < 8192; page++ {
		q.Set("cursor", cursor)
		raw, err := client.Request(ctx, "GET", path+"?"+q.Encode(), token, nil)
		if err != nil {
			return nil, nil, err
		}
		var data struct {
			Fields []contentField `json:"fields"`
			Items  []struct {
				contentField
				Offset int    `json:"offset"`
				Text   string `json:"text"`
			} `json:"items"`
			Page filePage `json:"page"`
		}
		var meta map[string]any
		if json.Unmarshal(raw, &data) != nil || json.Unmarshal(raw, &meta) != nil {
			return nil, nil, errors.New("invalid field response")
		}
		if page == 0 {
			fields, metadata = data.Fields, meta
			version, revision = text(meta["readingVersion"]), text(asObject(meta["revision"])["revision_id"])
			if len(fields) > 128 {
				return nil, nil, errors.New("too many fields")
			}
		} else if !reflect.DeepEqual(fields, data.Fields) || version != text(meta["readingVersion"]) ||
			revision != text(asObject(meta["revision"])["revision_id"]) {
			return nil, nil, errors.New("content changed during export")
		}
		for _, s := range data.Items {
			if !slices.Contains(fields, s.contentField) || utf8.RuneCountInString(texts[s.Path]) != s.Offset ||
				!utf8.ValidString(s.Text) || s.Offset+utf8.RuneCountInString(s.Text) > s.Length {
				return nil, nil, errors.New("invalid field offset or schema")
			}
			total += len(s.Text)
			if total > maxGraphBytes {
				return nil, nil, errors.New("export content exceeds limit")
			}
			texts[s.Path] += s.Text
		}
		if !data.Page.More {
			if data.Page.Next != "" || data.Page.End != "complete" {
				return nil, nil, errors.New("invalid field termination")
			}
			out := map[string]any{}
			for _, f := range fields {
				if utf8.RuneCountInString(texts[f.Path]) != f.Length {
					return nil, nil, errors.New("incomplete field")
				}
				if f.Encoding == "text" {
					out[f.Path] = texts[f.Path]
				} else if f.Encoding == "json" {
					var v any
					if json.Unmarshal([]byte(texts[f.Path]), &v) != nil {
						return nil, nil, errors.New("incomplete structured field")
					}
					out[f.Path] = v
				} else {
					return nil, nil, errors.New("unknown field encoding")
				}
			}
			return metadata, out, nil
		}
		if data.Page.Next == "" || data.Page.Next == cursor {
			return nil, nil, errors.New("invalid field continuation")
		}
		cursor = data.Page.Next
	}
	return nil, nil, errors.New("field pagination exceeds limit")
}

func readCollection(ctx context.Context, client *transport.Client, token, path string, q url.Values, version string) ([]map[string]any, error) {
	if q == nil {
		q = url.Values{}
	}
	items := []map[string]any{}
	cursor, pinned := "", ""
	seen := map[string]bool{}
	for page := 0; page < 512; page++ {
		q.Set("cursor", cursor)
		raw, err := client.Request(ctx, "GET", path+"?"+q.Encode(), token, nil)
		if err != nil {
			return nil, err
		}
		var data struct {
			Items          []map[string]any `json:"items"`
			Page           filePage         `json:"page"`
			ReadingVersion string           `json:"readingVersion"`
			Revision       map[string]any   `json:"revision"`
		}
		if json.Unmarshal(raw, &data) != nil {
			return nil, errors.New("invalid collection")
		}
		current := data.ReadingVersion
		if current == "" {
			current = text(data.Revision["revision_id"])
		}
		if page == 0 {
			pinned = current
		}
		if current != pinned || version != "" && current != version {
			return nil, errors.New("collection changed during export")
		}
		for _, item := range data.Items {
			identity := text(item["id"])
			if identity == "" {
				identity = text(item["file_ref_id"])
			}
			if identity == "" {
				identity = fmt.Sprint(item["ordinal"])
			}
			if seen[identity] {
				return nil, errors.New("duplicate collection item")
			}
			seen[identity] = true
			items = append(items, item)
		}
		if !data.Page.More {
			if data.Page.Next != "" || data.Page.End != "complete" {
				return nil, errors.New("incomplete collection")
			}
			return items, nil
		}
		if data.Page.Next == "" || data.Page.Next == cursor {
			return nil, errors.New("invalid collection continuation")
		}
		cursor = data.Page.Next
	}
	return nil, errors.New("collection pagination exceeds limit")
}

func exportFile(ctx context.Context, client *transport.Client, token, kind string, in fileInput) (map[string]any, error) {
	if in.Output == "" || in.File != "" || in.Progress != "" || in.RequestKey != "" || in.InspectOnly ||
		!slices.Contains([]string{".json", ".png", ".charx", ".starry"}, strings.ToLower(filepath.Ext(in.Output))) ||
		kind == "worldbook" && filepath.Ext(in.Output) != ".json" {
		return nil, errors.New("invalid export; standalone worldbook format is JSON")
	}
	if _, err := os.Lstat(in.Output); !os.IsNotExist(err) {
		return nil, errors.New("export output already exists or cannot be checked")
	}
	stage, err := os.MkdirTemp(filepath.Dir(in.Output), ".starry-export-*")
	if err != nil {
		return nil, errors.New("output parent must exist and be writable")
	}
	defer os.RemoveAll(stage)
	graph := fileGraph{Worldbooks: []map[string]any{}, Bindings: []map[string]any{},
		Relations: []map[string]any{}, Media: []map[string]any{}, Warnings: []string{}}
	versions := map[string]string{}
	mediaHashes := map[string]bool{}
	addMedia := func(hash, mime, role, ownerID string, download assets.DownloadInput) error {
		if !slices.Contains([]string{"avatar", "cover", "illustration", "background"}, role) || !assets.MediaHash.MatchString(hash) {
			return errors.New("unsupported or incomplete package media")
		}
		path := filepath.Join(stage, hash)
		if !mediaHashes[hash] {
			download.Output = path
			if _, err := assets.Download(ctx, client, token, download); err != nil {
				return err
			}
			graph.Media = append(graph.Media, map[string]any{"sha256": hash, "mediaType": mime, "file": path})
			mediaHashes[hash] = true
		}
		if ownerID == text(graph.Character["id"]) {
			graph.Relations = append(graph.Relations, map[string]any{"role": role, "target_type": "asset", "target_id": hash})
		} else {
			for _, book := range graph.Worldbooks {
				if text(book["id"]) == ownerID {
					ext := asObject(book["extensions"])
					if ext == nil {
						ext = map[string]any{}
						book["extensions"] = ext
					}
					media := asObject(ext["starry_media"])
					if media == nil {
						media = map[string]any{}
						ext["starry_media"] = media
					}
					media[role] = hash
				}
			}
		}
		return nil
	}
	var workspace func(string, string) (map[string]any, error)
	workspace = func(t, id string) (map[string]any, error) {
		path := "/api/v1/reading/catalog/" + t + "s/" + id
		meta, values, err := readContent(ctx, client, token, path, nil)
		if err != nil {
			return nil, err
		}
		versions[path] = text(meta["readingVersion"])
		if issues, ok := values["exportIssues"].([]any); !ok || len(issues) != 0 {
			return nil, errors.New("workspace graph contains unsupported or unavailable presentation resources")
		}
		values["id"] = id
		if t == "worldbook" {
			entries, err := readCollection(ctx, client, token, path+"/entries", nil, "")
			if err != nil {
				return nil, err
			}
			full := []any{}
			for _, entry := range entries {
				_, fields, err := readContent(ctx, client, token, path+"/entries/"+text(entry["id"]), nil)
				if err != nil {
					return nil, err
				}
				full = append(full, fields)
			}
			values["entries"] = full
			graph.Worldbooks = append(graph.Worldbooks, values)
		} else {
			graph.Character = values
			bindings, err := readCollection(ctx, client, token, path+"/bindings", nil, "")
			if err != nil {
				return nil, err
			}
			for _, b := range bindings {
				fields := asObject(b["metadata"])
				if fields["enabled"] == false {
					return nil, errors.New("disabled bindings cannot be represented by this export")
				}
				target := text(fields["worldbook_id"])
				if !assets.UUID.MatchString(target) {
					return nil, errors.New("unresolved worldbook binding")
				}
				if _, err := workspace("worldbook", target); err != nil {
					return nil, err
				}
				graph.Bindings = append(graph.Bindings, map[string]any{"worldbook_id": target, "reference_type": fields["reference_type"]})
			}
		}
		if media, ok := values["media"].([]any); ok {
			for _, raw := range media {
				m := asObject(raw)
				role := text(m["role"])
				if err := addMedia(text(m["hash"]), "", role, id,
					assets.DownloadInput{Source: "workspace", Type: t, ID: id, MediaRole: role}); err != nil {
					return nil, err
				}
			}
		}
		return values, nil
	}
	var formal func(assets.ResourceInput, bool) (map[string]any, error)
	formal = func(address assets.ResourceInput, root bool) (map[string]any, error) {
		input := assets.PackageInput{ResourceInput: address, Profile: "full_package", Group: "all"}
		path, err := assets.PackagePath(input)
		if err != nil {
			return nil, err
		}
		parsed, _ := url.Parse(path)
		meta, values, err := readContent(ctx, client, token, parsed.Path, parsed.Query())
		if err != nil {
			return nil, err
		}
		address.Revision = text(asObject(meta["revision"])["revision_id"])
		if !assets.UUID.MatchString(address.Revision) {
			return nil, errors.New("export requires fixed revision")
		}
		id := text(asObject(meta["asset"])["repository_id"])
		if id == "" {
			return nil, errors.New("invalid published identity")
		}
		values["id"] = id
		links, _ := values["linked_assets"].([]any)
		if integer(values["dependencyCount"]) != int64(len(links)) {
			return nil, errors.New("export blocked by inaccessible dependencies")
		}
		if address.Type == "character" {
			graph.Character = values
			for _, raw := range links {
				link := asObject(raw)
				if text(link["asset_type"]) != "worldbook" || !slices.Contains([]string{"embedded", "reference"}, text(link["relationship"])) {
					return nil, errors.New("unsupported formal dependency")
				}
				target := assets.ResourceInput{Type: "worldbook", Owner: text(link["owner_user_id"]),
					Slug: text(link["slug"]), Revision: text(link["resolved_revision_id"])}
				book, err := formal(target, false)
				if err != nil {
					return nil, err
				}
				graph.Bindings = append(graph.Bindings, map[string]any{"worldbook_id": book["id"], "reference_type": link["relationship"]})
			}
		} else {
			for _, raw := range links {
				link := asObject(raw)
				if root || link["asset_type"] != "character" || link["owner_user_id"] != in.Owner || link["slug"] != in.Slug {
					return nil, errors.New("unsupported worldbook dependency")
				}
			}
			entryPath, err := assets.PackagePath(assets.PackageInput{ResourceInput: address, Profile: "full_package", Group: "entries"})
			if err != nil {
				return nil, err
			}
			u, _ := url.Parse(entryPath)
			entries, err := readCollection(ctx, client, token, u.Path, u.Query(), address.Revision)
			if err != nil {
				return nil, err
			}
			full := []any{}
			for _, e := range entries {
				entryPath, _ := assets.PackagePath(assets.PackageInput{ResourceInput: address, Profile: "full_package", Group: "entry", EntryOrdinal: int(integer(e["ordinal"]))})
				u, _ := url.Parse(entryPath)
				_, fields, err := readContent(ctx, client, token, u.Path, u.Query())
				if err != nil {
					return nil, err
				}
				full = append(full, fields)
			}
			values["entries"] = full
			graph.Worldbooks = append(graph.Worldbooks, values)
		}
		filePath, _ := assets.PackagePath(assets.PackageInput{ResourceInput: address, Profile: "full_package", Group: "mediaFiles"})
		u, _ := url.Parse(filePath)
		files, err := readCollection(ctx, client, token, u.Path, u.Query(), address.Revision)
		if err != nil {
			return nil, err
		}
		if integer(values["mediaFileCount"]) != int64(len(files)) {
			return nil, errors.New("export blocked by unavailable or restricted files")
		}
		roles := map[string]bool{}
		for _, f := range files {
			role, err := publishedMediaSlot(f)
			if err != nil {
				return nil, err
			}
			if roles[role] {
				return nil, errors.New("multiple files cannot fit one media slot")
			}
			roles[role] = true
			if err := addMedia(text(f["sha256"]), text(f["mime_type"]), role, id,
				assets.DownloadInput{Source: "published", Type: address.Type, Owner: address.Owner,
					Slug: address.Slug, Revision: address.Revision, FileRefID: text(f["file_ref_id"])}); err != nil {
				return nil, err
			}
		}
		return values, nil
	}
	switch in.Source {
	case "workspace":
		if !assets.UUID.MatchString(in.ID) || in.Owner != "" || in.Slug != "" || in.Revision != "" {
			return nil, errors.New("workspace export requires only owned draft ID")
		}
		if _, err := workspace(kind, in.ID); err != nil {
			return nil, err
		}
		for path, version := range versions {
			meta, _, err := readContent(ctx, client, token, path, nil)
			if err != nil {
				return nil, err
			}
			if text(meta["readingVersion"]) != version {
				return nil, errors.New("workspace changed during export")
			}
		}
	case "published":
		if in.ID != "" {
			return nil, errors.New("published export uses owner, slug and revision")
		}
		if _, err := formal(assets.ResourceInput{Type: kind, Owner: in.Owner, Slug: in.Slug, Revision: in.Revision}, true); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("export source must be workspace or published")
	}
	graphPath := filepath.Join(stage, "graph.json")
	if err := saveProgress(graphPath, graph); err != nil {
		return nil, err
	}
	output := filepath.Join(stage, "output"+strings.ToLower(filepath.Ext(in.Output)))
	if err := formatHost(ctx, "export", kind, graphPath, output); err != nil {
		return nil, err
	}
	if _, err := validateSource(output, kind); err != nil {
		return nil, err
	}
	if err := os.Link(output, in.Output); err != nil {
		return nil, errors.New("cannot finalize export; output must not exist")
	}
	return map[string]any{"data": map[string]any{"state": "exported", "output": in.Output,
		"worldbooks": len(graph.Worldbooks), "media": len(graph.Media)}}, nil
}
