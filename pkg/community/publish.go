package community

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
)

type PublishSelection struct {
	PublishAs    string `json:"publishAs" jsonschema:"Explicit signing mode: owner or ai. Comments require owner."`
	AIIdentityID string `json:"aiIdentityId,omitempty" jsonschema:"Owner-approved subordinate AI identity ID; required only for ai mode."`
}
type MediaRef struct {
	AssetID    string `json:"assetId,omitempty" jsonschema:"Existing owner-controlled CAS asset ID; do not combine with fileId or storageKey."`
	FileID     string `json:"fileId,omitempty" jsonschema:"Existing upload file ID, together with storageKey."`
	StorageKey string `json:"storageKey,omitempty"`
	Width      *int   `json:"width,omitempty"`
	Height     *int   `json:"height,omitempty"`
}
type DynamicCreateInput struct {
	PublishSelection
	RequestKey  string     `json:"requestKey" jsonschema:"Stable key for this one publishing intent. Reuse unchanged after timeout; never replace to resolve unknown outcome."`
	NodeID      string     `json:"nodeId,omitempty" jsonschema:"Existing content node ID as a decimal string; omit to use backend default."`
	Title       string     `json:"title,omitempty"`
	Content     string     `json:"content"`
	ContentType string     `json:"contentType,omitempty" jsonschema:"Plain text only (text). Markdown and HTML markers remain literal; omitted format is text."`
	Tags        []string   `json:"tags,omitempty"`
	HideContent string     `json:"hideContent,omitempty"`
	ImageList   []MediaRef `json:"imageList,omitempty" jsonschema:"Previously uploaded media references owned by this account; this tool does not upload files."`
}
type ArticleCreateInput struct {
	PublishSelection
	RequestKey  string    `json:"requestKey" jsonschema:"Stable publishing intent key; reuse unchanged after timeout."`
	NodeID      string    `json:"nodeId,omitempty"`
	Title       string    `json:"title"`
	Summary     string    `json:"summary,omitempty"`
	Content     string    `json:"content"`
	ContentType string    `json:"contentType,omitempty" jsonschema:"text or markdown"`
	Tags        []string  `json:"tags,omitempty"`
	SourceURL   string    `json:"sourceUrl,omitempty"`
	Cover       *MediaRef `json:"cover,omitempty"`
}
type CommentCreateInput struct {
	PublishSelection
	RequestKey string `json:"requestKey" jsonschema:"Stable publishing intent key; reuse unchanged after timeout."`
	EntityType string `json:"entityType" jsonschema:"Target: dynamic, article, or comment."`
	EntityID   string `json:"entityId" jsonschema:"Positive target ID as a decimal string."`
	Content    string `json:"content"`
	QuoteID    string `json:"quoteId,omitempty"`
}
type OperationInput struct {
	OperationID string `json:"operationId,omitempty" jsonschema:"Operation ID from a successful create; otherwise use action and requestKey."`
	Action      string `json:"action,omitempty" jsonschema:"dynamic.create, article.create, or comment.create"`
	RequestKey  string `json:"requestKey,omitempty"`
}

var requestKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func validOptionalID(id string) bool {
	return id == "" || id == "0" || validID(id)
}

func validSelection(selection PublishSelection) bool {
	return selection.PublishAs == "owner" && selection.AIIdentityID == "" ||
		selection.PublishAs == "ai" && selection.AIIdentityID != ""
}

func operationPath(input OperationInput) (string, error) {
	if input.OperationID != "" {
		if input.Action != "" || input.RequestKey != "" || !requestKeyPattern.MatchString(input.OperationID) {
			return "", errors.New("invalid operation arguments")
		}
		return "/api/v1/integration-operations/" + url.PathEscape(input.OperationID), nil
	}
	if !slices.Contains([]string{"dynamic.create", "article.create", "comment.create"}, input.Action) || !requestKeyPattern.MatchString(input.RequestKey) {
		return "", errors.New("invalid operation arguments")
	}
	return "/api/v1/integration-operations?" + url.Values{"action": {input.Action}, "requestKey": {input.RequestKey}}.Encode(), nil
}

func (b *Client) publish(ctx context.Context, token, action, key string, input any) (map[string]any, error) {
	if !requestKeyPattern.MatchString(key) {
		return nil, errors.New("invalid requestKey")
	}
	path := map[string]string{"dynamic.create": "/api/v1/community/posts/dynamic", "article.create": "/api/v1/community/posts/article", "comment.create": "/api/v1/community/comments"}[action]
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &body); err != nil {
		return nil, err
	}
	delete(body, "requestKey")
	data, err := b.RequestWithKey(ctx, "POST", path, token, key, body)
	if err != nil {
		var api *APIError
		if errors.As(err, &api) && api.Status >= 500 {
			return map[string]any{
				"state": "outcome_unknown", "action": action, "requestKey": key,
				"next": "Query community_get_operation with the same action/requestKey, or retry the identical payload with the original requestKey. Do not generate a new key.",
			}, nil
		}
		return nil, err
	}
	data, err = b.operationLink(data, action)
	return map[string]any{"data": data}, err
}

func (b *Client) operationLink(data json.RawMessage, action string) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, newAPIError(503, "upstream_unavailable")
	}
	b.AddOperationLink(value, action)
	return json.Marshal(value)
}

func (b *Client) AddOperationLink(value any, action string) {
	row, ok := value.(map[string]any)
	if !ok {
		return
	}
	if stored, ok := row["action"].(string); ok {
		action = stored
	}
	kind := map[string]string{"dynamic.create": "dynamic", "article.create": "article"}[action]
	raw, _ := row["entityId"].(string)
	if kind == "" || !validID(raw) {
		return
	}
	row["link"] = b.publicWeb + "/cluster/" + kind + "/" + raw
}
