package community

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/allenvissong-source/starry-cli/pkg/assets"
	"github.com/allenvissong-source/starry-cli/pkg/catalog"
	"github.com/allenvissong-source/starry-cli/pkg/transport"
	"slices"
	"strings"
)

// DecodeInput 不允许未知字段、第二个 JSON 或精度损失。
func DecodeInput(data json.RawMessage, out any) error {
	return transport.DecodeInput(data, out)
}

func inputPath[T any](data json.RawMessage, build func(T) (string, error)) (string, error) {
	var input T
	if err := DecodeInput(data, &input); err != nil {
		return "", err
	}
	return build(input)
}

// Call 是 MCP 和 CLI 的同源入口。后端仍是权限与业务语义的权威。
func (b *Client) Call(ctx context.Context, token, name string, data json.RawMessage) (map[string]any, error) {
	var path string
	var err error
	switch name {
	case "ai_identity_list", "ai_identity_create", "ai_identity_update":
		return b.callAI(ctx, token, name, data)
	case "account_get_identity":
		path, err = inputPath(data, func(struct{}) (string, error) { return "/api/v1/integration-context", nil })
	case "community_list_posts":
		path, err = inputPath(data, listPath)
	case "community_search_posts":
		path, err = inputPath(data, searchPath)
	case "community_list_topics":
		path, err = inputPath(data, topicsPath)
	case "community_get_post":
		path, err = inputPath(data, postPath)
	case "community_list_comments":
		path, err = inputPath(data, commentPath)
	case "community_get_my_stats":
		path, err = inputPath(data, statsPath)
	case "community_list_notifications":
		path, err = inputPath(data, notificationsPath)
	case "community_get_inbox":
		path, err = inputPath(data, func(struct{}) (string, error) { return "/api/v1/community/inbox", nil })
	case "community_get_operation":
		path, err = inputPath(data, operationPath)
	case "community_create_dynamic":
		var input DynamicCreateInput
		if err = DecodeInput(data, &input); err != nil {
			return nil, err
		}
		if !validSelection(input.PublishSelection) || !validOptionalID(input.NodeID) || input.Content == "" {
			return nil, errors.New("invalid dynamic arguments")
		}
		return b.publish(ctx, token, "dynamic.create", input.RequestKey, input)
	case "community_create_article":
		var input ArticleCreateInput
		if err = DecodeInput(data, &input); err != nil {
			return nil, err
		}
		if !validSelection(input.PublishSelection) || !validOptionalID(input.NodeID) || input.Title == "" || input.Content == "" {
			return nil, errors.New("invalid article arguments")
		}
		return b.publish(ctx, token, "article.create", input.RequestKey, input)
	case "community_create_comment":
		var input CommentCreateInput
		if err = DecodeInput(data, &input); err != nil {
			return nil, err
		}
		if input.PublishAs != "owner" || !validSelection(input.PublishSelection) || !validID(input.EntityID) || !validOptionalID(input.QuoteID) || input.Content == "" ||
			!slices.Contains([]string{"dynamic", "article", "comment"}, input.EntityType) {
			return nil, errors.New("invalid comment arguments")
		}
		return b.publish(ctx, token, "comment.create", input.RequestKey, input)
	default:
		if assets.IsCollaboration(name) {
			return assets.Collaboration(ctx, b.Client, token, name, data)
		}
		if name == "assets_prepare_upload" || name == "assets_finalize_upload" || name == "assets_get_upload" {
			return assets.Media(ctx, b.Client, token, name, data)
		}
		if assets.IsPublication(name) {
			return assets.Publication(ctx, b.Client, token, name, data)
		}
		if name == "assets_get_operation" {
			return catalog.Operation(ctx, b.Client, token, data)
		}
		if strings.HasPrefix(name, "assets_") {
			return assets.Call(ctx, b.Client, token, name, data)
		}
		if strings.HasPrefix(name, "catalog_") {
			return catalog.Call(ctx, b.Client, token, name, data)
		}
		return nil, errors.New("unknown command")
	}
	if err != nil {
		return nil, err
	}
	result, err := b.Request(ctx, "GET", path, token, nil)
	if err != nil {
		return nil, err
	}
	if name == "community_get_operation" {
		result, err = b.operationLink(result, "")
	}
	return map[string]any{"data": result}, err
}

func ErrorPayload(err error) map[string]any {
	api := &APIError{Code: "invalid_argument", Message: err.Error()}
	var upstream *APIError
	if errors.As(err, &upstream) {
		api = upstream
	} else if errors.Is(err, context.Canceled) {
		api = &APIError{Code: "request_canceled", Message: context.Canceled.Error()}
	} else if errors.Is(err, context.DeadlineExceeded) {
		api = &APIError{Code: "deadline_exceeded", Message: context.DeadlineExceeded.Error()}
	}
	return map[string]any{"error": api}
}
