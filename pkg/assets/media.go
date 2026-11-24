package assets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"crypto/sha256"
	"encoding/hex"
	"github.com/allenvissong-source/starry-cli/pkg/transport"
)

const MaxMediaBytes int64 = 128 << 20

var MediaHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// The handle is a digest of host-managed bytes, never a file path or URL.
type MediaInput struct {
	SHA256     string `json:"sha256" jsonschema:"Bare lowercase SHA256 handle; no URL or local path"`
	MediaType  string `json:"mediaType" jsonschema:"image/png, image/jpeg, image/gif or image/webp"`
	ByteSize   int64  `json:"byteSize" jsonschema:"1-134217728; backend storage size is authoritative"`
	RequestKey string `json:"requestKey" jsonschema:"Stable CAS registration mutation ID; preserve on uncertain outcome"`
	Ticket     string `json:"ticket,omitempty" jsonschema:"Direct upload ticket from prepare; finalize with matching receipt"`
	Receipt    string `json:"receipt,omitempty" jsonschema:"Verified edge receipt; required with ticket"`
}

func validMedia(in MediaInput) bool {
	return MediaHash.MatchString(in.SHA256) && in.ByteSize > 0 && in.ByteSize <= MaxMediaBytes &&
		slices.Contains([]string{"image/png", "image/jpeg", "image/gif", "image/webp"}, in.MediaType) &&
		publicationIntent.MatchString(in.RequestKey)
}

func Media(ctx context.Context, client *transport.Client, token, name string, raw json.RawMessage) (map[string]any, error) {
	var in MediaInput
	if transport.DecodeInput(raw, &in) != nil || !validMedia(in) {
		return nil, errors.New("invalid media handle")
	}
	switch name {
	case "assets_get_upload":
		return transport.ReadingResult(client.Request(ctx, "GET", "/api/v1/authoring/cas/assets?"+url.Values{
			"requestKey": {in.RequestKey}, "sha256": {in.SHA256}}.Encode(), token, nil))
	case "assets_prepare_upload":
		if in.Ticket != "" || in.Receipt != "" {
			return nil, errors.New("prepare does not accept upload receipts")
		}
		response, err := client.Do(ctx, "GET", "/api/v1/authoring/cas/blobs/"+in.SHA256, token, "", nil)
		present := err == nil
		var api *transport.APIError
		if err != nil && (!errors.As(err, &api) || api.Status != 404 || api.Code != "cas.blob_not_found") {
			return nil, err
		}
		if present {
			var probe struct {
				SHA256 string `json:"sha256"`
				Size   int64  `json:"byte_size"`
			}
			if json.Unmarshal(response.Data, &probe) != nil || probe.SHA256 != in.SHA256 || probe.Size != in.ByteSize {
				return nil, errors.New("stored media size or digest mismatch")
			}
		}
		data := map[string]any{"present": present, "sha256": in.SHA256, "byteSize": in.ByteSize}
		if !present {
			prepared, err := client.Do(ctx, "POST", "/api/v1/authoring/cas/blobs/"+in.SHA256+"/prepare", token, "",
				map[string]any{"byte_size": in.ByteSize, "media_type": in.MediaType})
			if err != nil {
				return nil, err
			}
			var plan struct {
				URL    string `json:"upload_url"`
				Method string `json:"method"`
				Ticket string `json:"ticket"`
			}
			if json.Unmarshal(prepared.Data, &plan) != nil || plan.Method != "PUT" || !validDirectPlan(client, plan.URL, plan.Ticket) {
				return nil, errors.New("invalid direct upload plan")
			}
			data["uploadUrl"], data["method"], data["ticket"] = plan.URL, plan.Method, plan.Ticket
		}
		return map[string]any{"data": data}, nil
	case "assets_finalize_upload":
		if (in.Ticket == "") != (in.Receipt == "") || len(in.Ticket) > 4096 || len(in.Receipt) > 4096 {
			return nil, errors.New("invalid direct upload receipt")
		}
		if in.Ticket != "" {
			completed, err := client.Do(ctx, "POST", "/api/v1/authoring/cas/blobs/"+in.SHA256+"/complete", token, "",
				map[string]any{"ticket": in.Ticket, "receipt": in.Receipt})
			if err != nil {
				return nil, err
			}
			var blob struct {
				SHA256 string `json:"sha256"`
				Size   int64  `json:"byte_size"`
			}
			if json.Unmarshal(completed.Data, &blob) != nil || blob.SHA256 != in.SHA256 || blob.Size != in.ByteSize {
				return nil, errors.New("completed upload does not match media")
			}
		}
		result, err := transport.WriteResult(client.Do(ctx, "POST", "/api/v1/authoring/cas/assets", token, in.RequestKey,
			map[string]any{"sha256": in.SHA256, "media_type": in.MediaType,
				"byte_size": in.ByteSize, "client_mutation_id": in.RequestKey}))
		if err != nil {
			var api *transport.APIError
			if errors.As(err, &api) && api.Code == "outcome_unknown" {
				recovery, _ := json.Marshal(map[string]any{"sha256": in.SHA256, "mediaType": in.MediaType,
					"byteSize": in.ByteSize, "requestKey": in.RequestKey})
				copyError := *api
				copyError.Message = "Query media operation with " + string(recovery)
				return nil, &copyError
			}
			return nil, err
		}
		data, ok := result["data"].(map[string]any)
		id, _ := data["asset_id"].(string)
		if !ok || !UUID.MatchString(id) || data["sha256"] != in.SHA256 {
			return nil, &transport.APIError{Status: 503, Code: "outcome_unknown", Message: "Preserve CAS registration key and digest", Retryable: false}
		}
		return result, nil
	default:
		return nil, errors.New("unknown media operation")
	}
}

func validDirectPlan(client *transport.Client, href, ticket string) bool {
	u, err := url.Parse(href)
	return err == nil && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" &&
		u.Scheme+"://"+u.Host == client.CASDirectUploadOrigin() && u.RawPath == "" &&
		ticket != "" && len(ticket) <= 4096 && !strings.ContainsAny(ticket, "/\\\r\n") && u.Path == "/uploads/"+ticket
}

type DownloadInput struct {
	Source    string `json:"source"`
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	MediaRole string `json:"mediaRole,omitempty"`
	Owner     string `json:"owner,omitempty"`
	Slug      string `json:"slug,omitempty"`
	Revision  string `json:"revision,omitempty"`
	FileRefID string `json:"fileRefId,omitempty"`
	Output    string `json:"output"`
}

func Download(ctx context.Context, client *transport.Client, token string, in DownloadInput) (map[string]any, error) {
	if (in.Type != "character" && in.Type != "worldbook") || in.Output == "" {
		return nil, errors.New("invalid media download")
	}
	digest, mimeType, href := "", "", ""
	if in.Source == "workspace" {
		if !UUID.MatchString(in.ID) || !slices.Contains([]string{"avatar", "cover", "illustration", "background"}, in.MediaRole) ||
			in.Owner != "" || in.Slug != "" || in.Revision != "" || in.FileRefID != "" {
			return nil, errors.New("workspace download needs only id and mediaRole")
		}
		raw, err := client.Request(ctx, "GET", "/api/v1/reading/catalog/"+in.Type+"s/"+in.ID+"/media/"+in.MediaRole, token, nil)
		if err != nil {
			return nil, err
		}
		var target struct {
			Hash, Href string
			AssetID    string `json:"assetId"`
		}
		if json.Unmarshal(raw, &target) != nil || !UUID.MatchString(target.AssetID) {
			return nil, errors.New("invalid media delivery")
		}
		digest, href = target.Hash, target.Href
	} else if in.Source == "published" {
		if in.ID != "" || in.MediaRole != "" || !UUID.MatchString(in.FileRefID) || !UUID.MatchString(in.Revision) ||
			!transport.ValidUserID(in.Owner) || !slugID.MatchString(in.Slug) {
			return nil, errors.New("published download needs owner, slug, fixed revision and fileRefId")
		}
		base := "/api/v1/reading/assets/" + in.Type + "/" + url.PathEscape(in.Owner) + "/" + url.PathEscape(in.Slug) + "/media"
		cursor := ""
		found := false
		for page := 0; page < 512; page++ {
			raw, err := client.Request(ctx, "GET", base+"?"+url.Values{"revision": {in.Revision}, "cursor": {cursor}}.Encode(), token, nil)
			if err != nil {
				return nil, err
			}
			var files struct {
				Items []struct {
					ID   string `json:"file_ref_id"`
					Hash string `json:"sha256"`
					Type string `json:"mime_type"`
				} `json:"items"`
				Page struct {
					HasMore bool   `json:"hasMore"`
					Next    string `json:"nextCursor"`
				} `json:"page"`
			}
			if json.Unmarshal(raw, &files) != nil {
				return nil, errors.New("invalid media list")
			}
			for _, file := range files.Items {
				if file.ID == in.FileRefID {
					digest, mimeType, found = file.Hash, file.Type, true
				}
			}
			if found || !files.Page.HasMore {
				break
			}
			if files.Page.Next == "" || files.Page.Next == cursor {
				return nil, errors.New("invalid media continuation")
			}
			cursor = files.Page.Next
		}
		if !found {
			return nil, errors.New("media reference not in fixed revision")
		}
		raw, err := client.Request(ctx, "GET", base+"/"+in.FileRefID+"?revision="+in.Revision, token, nil)
		if err != nil {
			return nil, err
		}
		var target struct {
			Href      string
			FileRefID string `json:"fileRefId"`
		}
		if json.Unmarshal(raw, &target) != nil || target.FileRefID != in.FileRefID {
			return nil, errors.New("invalid media delivery")
		}
		href = target.Href
	} else {
		return nil, errors.New("download source must be workspace or published")
	}
	if err := DownloadURL(ctx, client, href, digest, mimeType, in.Output); err != nil {
		return nil, err
	}
	return map[string]any{"data": map[string]any{"output": in.Output, "sha256": digest, "state": "downloaded"}}, nil
}

// UploadFile hashes and type-checks the same open regular file twice; modifying
// it between inspection and upload is detected by both client and server.
func UploadFile(ctx context.Context, client *transport.Client, token, path, key string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open media file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > MaxMediaBytes {
		return nil, errors.New("media must be a regular file within 128 MiB")
	}
	header := make([]byte, 512)
	n, _ := io.ReadFull(f, header)
	in := MediaInput{MediaType: http.DetectContentType(header[:n]), ByteSize: info.Size(), RequestKey: key}
	f.Seek(0, io.SeekStart)
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(f, MaxMediaBytes+1))
	in.SHA256 = hex.EncodeToString(hash.Sum(nil))
	if err != nil || size != info.Size() || !validMedia(in) {
		return nil, errors.New("unsupported media type, size or intent key")
	}
	raw, _ := json.Marshal(in)
	prepared, err := Media(ctx, client, token, "assets_prepare_upload", raw)
	if err != nil {
		return nil, err
	}
	if !prepared["data"].(map[string]any)["present"].(bool) {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		plan := prepared["data"].(map[string]any)
		in.Ticket = plan["ticket"].(string)
		response, err := client.UploadDirect(ctx, plan["uploadUrl"].(string), in.Ticket, in.MediaType, size, f)
		if err != nil {
			return nil, err
		}
		var uploaded struct {
			SHA256  string `json:"sha256"`
			Size    int64  `json:"byte_size"`
			Receipt string `json:"receipt"`
		}
		if (response.Status != 200 && response.Status != 201) || json.Unmarshal(response.Data, &uploaded) != nil ||
			uploaded.SHA256 != in.SHA256 || uploaded.Size != size || uploaded.Receipt == "" || len(uploaded.Receipt) > 4096 {
			return nil, errors.New("upload response does not match media")
		}
		in.Receipt = uploaded.Receipt
		raw, _ = json.Marshal(in)
	}
	return Media(ctx, client, token, "assets_finalize_upload", raw)
}

// DownloadURL consumes only a URL resolved by a permission-checked resource
// endpoint. No bearer credentials follow that URL, even on the same origin.
// The output is linked into place only after size and digest verification.
func DownloadURL(ctx context.Context, client *transport.Client, href, digest, mediaType, output string) error {
	u, err := url.Parse(href)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" ||
		(u.Scheme != "https" && !(u.Scheme == "http" && slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, u.Hostname()))) ||
		!MediaHash.MatchString(digest) || strings.TrimSpace(output) == "" {
		return errors.New("invalid approved media delivery")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		return errors.New("output already exists or cannot be checked")
	}
	f, err := os.CreateTemp(filepath.Dir(output), ".starry-download-*")
	if err != nil {
		return errors.New("output parent must exist and be writable")
	}
	defer os.Remove(f.Name())
	defer f.Close()
	req, _ := http.NewRequestWithContext(ctx, "GET", href, nil)
	resp, err := client.HTTPClient().Do(req)
	if err != nil {
		return transport.NewAPIError(503, "upstream_unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.ContentLength > MaxMediaBytes {
		return errors.New("media delivery unavailable or too large")
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(resp.Body, MaxMediaBytes+1))
	if err != nil || n < 1 || n > MaxMediaBytes || hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("media delivery is incomplete or has wrong digest")
	}
	if mediaType != "" {
		f.Seek(0, io.SeekStart)
		header := make([]byte, 512)
		length, _ := f.Read(header)
		if http.DetectContentType(header[:length]) != mediaType {
			return errors.New("media delivery type mismatch")
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Link(f.Name(), output); err != nil {
		return fmt.Errorf("cannot finalize media output: %w", err)
	}
	return nil
}
