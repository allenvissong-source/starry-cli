package community

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"
)

type AIIdentityCreateInput struct {
	RequestKey  string `json:"requestKey" jsonschema:"Stable idempotency key; retain the same key and complete payload when retrying"`
	Name        string `json:"name" jsonschema:"Public AI name, 1-80 Unicode code points"`
	Avatar      string `json:"avatar,omitempty" jsonschema:"Optional HTTPS avatar URL"`
	Description string `json:"description,omitempty" jsonschema:"Public profile, at most 1000 Unicode code points"`
}

type AIIdentityUpdateInput struct {
	ID          string  `json:"id" jsonschema:"ID of an allowed active identity created by this connection"`
	Name        *string `json:"name,omitempty" jsonschema:"New public name; omitted fields are preserved"`
	Avatar      *string `json:"avatar,omitempty" jsonschema:"HTTPS avatar URL, or empty to clear"`
	Description *string `json:"description,omitempty" jsonschema:"Public profile, or empty to clear"`
}

func validAIFields(name, avatar, description *string) bool {
	if name != nil && (strings.TrimSpace(*name) == "" || utf8.RuneCountInString(strings.TrimSpace(*name)) > 80) {
		return false
	}
	if avatar != nil && *avatar != "" {
		u, err := url.Parse(strings.TrimSpace(*avatar))
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(*avatar) > 2048 {
			return false
		}
	}
	return description == nil || utf8.RuneCountInString(*description) <= 1000
}

func (b *Client) ListAI(ctx context.Context, token string) (map[string]any, error) {
	data, err := b.Request(ctx, "GET", "/api/v1/integration/ai-identities", token, nil)
	return map[string]any{"data": data}, err
}

func (b *Client) CreateAI(ctx context.Context, token string, input AIIdentityCreateInput) (map[string]any, error) {
	if !requestKeyPattern.MatchString(input.RequestKey) || !validAIFields(&input.Name, &input.Avatar, &input.Description) {
		return nil, errors.New("invalid AI identity creation input")
	}
	body := map[string]string{"name": strings.TrimSpace(input.Name), "avatar": strings.TrimSpace(input.Avatar), "description": input.Description}
	data, err := b.RequestWithKey(ctx, "POST", "/api/v1/integration/ai-identities", token, input.RequestKey, body)
	if err != nil {
		// Creation changes the grant version. Retry with a refreshed access token
		// and the same intent after an ambiguous response.
		return nil, err
	}
	return map[string]any{"data": data, "refreshRequired": true}, nil
}

func (b *Client) UpdateAI(ctx context.Context, token string, input AIIdentityUpdateInput) (map[string]any, error) {
	if input.ID == "" || len(input.ID) > 128 || strings.ContainsAny(input.ID, "/?#% \t\r\n") ||
		input.Name == nil && input.Avatar == nil && input.Description == nil ||
		!validAIFields(input.Name, input.Avatar, input.Description) {
		return nil, errors.New("invalid AI identity update input")
	}
	body := map[string]string{}
	if input.Name != nil {
		body["name"] = strings.TrimSpace(*input.Name)
	}
	if input.Avatar != nil {
		body["avatar"] = strings.TrimSpace(*input.Avatar)
	}
	if input.Description != nil {
		body["description"] = *input.Description
	}
	data, err := b.Request(ctx, "PATCH", "/api/v1/integration/ai-identities/"+url.PathEscape(input.ID), token, body)
	return map[string]any{"data": data}, err
}

func (b *Client) callAI(ctx context.Context, token, name string, raw json.RawMessage) (map[string]any, error) {
	switch name {
	case "ai_identity_list":
		var input struct{}
		if err := DecodeInput(raw, &input); err != nil {
			return nil, err
		}
		return b.ListAI(ctx, token)
	case "ai_identity_create":
		var input AIIdentityCreateInput
		if err := DecodeInput(raw, &input); err != nil {
			return nil, err
		}
		return b.CreateAI(ctx, token, input)
	case "ai_identity_update":
		var input AIIdentityUpdateInput
		if err := DecodeInput(raw, &input); err != nil {
			return nil, err
		}
		return b.UpdateAI(ctx, token, input)
	}
	return nil, errors.New("unknown identity command")
}
