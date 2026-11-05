package assets

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"

	"starry-cli/pkg/transport"
)

type SuggestionsInput struct {
	ResourceInput
	Status string `json:"status,omitempty" jsonschema:"open, withdrawn, rejected or adopted"`
	Limit  *int   `json:"limit,omitempty" jsonschema:"1-20, default 10"`
	Cursor string `json:"cursor,omitempty" jsonschema:"Repeat all filters with this cursor"`
}

type SuggestionInput struct {
	ResourceInput
	SuggestionID string `json:"suggestionId" jsonschema:"Suggestion UUID; only its author and the resource owner may read"`
	Side         string `json:"side,omitempty" jsonschema:"Omit for metadata; base or proposal for fixed segmented content"`
	BodyLimit    *int   `json:"bodyLimit,omitempty" jsonschema:"1000-12000 Unicode scalars"`
	Cursor       string `json:"cursor,omitempty" jsonschema:"Continue the same fixed side"`
}

type NotificationsInput struct {
	Type   string `json:"type,omitempty" jsonschema:"character, worldbook or all; all includes only granted types"`
	Limit  *int   `json:"limit,omitempty" jsonschema:"1-20, default 10"`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque continuation; reading never marks human messages read"`
}

type StatsInput struct {
	Type string `json:"type" jsonschema:"character or worldbook; statistics cover only the owner's resources"`
}

type LinkedAssetChoice struct {
	ID   string `json:"id" jsonschema:"Exact linked_assets ID from the source manifest"`
	Mode string `json:"mode" jsonschema:"reference, fork or omit; fork currently preserves the immutable reference"`
}

type CollaborationInput struct {
	Type                   string              `json:"type" jsonschema:"character or worldbook"`
	RequestKey             string              `json:"requestKey" jsonschema:"Stable intent key; retain exact input on ambiguous failure"`
	Owner                  string              `json:"owner" jsonschema:"Base resource owner"`
	Slug                   string              `json:"slug" jsonschema:"Base resource slug"`
	ExpectedRevision       string              `json:"expectedRevision" jsonschema:"Required current base head UUID"`
	SuggestionID           string              `json:"suggestionId,omitempty"`
	ProposalContentItemID  string              `json:"proposalContentItemId,omitempty" jsonschema:"Own fork repository ID, not draft ID"`
	ProposalRevisionID     string              `json:"proposalRevisionId,omitempty" jsonschema:"Fixed published proposal revision"`
	Title                  string              `json:"title,omitempty"`
	Body                   string              `json:"body,omitempty"`
	LinkedAssets           []LinkedAssetChoice `json:"linkedAssets,omitempty"`
	PublishAs              string              `json:"publishAs,omitempty" jsonschema:"Adoption requires owner or ai; comment requires owner"`
	AIIdentityID           string              `json:"aiIdentityId,omitempty"`
	DraftID                string              `json:"draftId,omitempty" jsonschema:"Fork only: optional prepared unpublished owned workspace; fork attaches lineage but does not import content"`
	ExpectedDraftVersion   int64               `json:"expectedDraftVersion,omitempty"`
	ExpectedReadingVersion string              `json:"expectedReadingVersion,omitempty"`
}

func CollaborationAction(name string) string {
	return map[string]string{"assets_fork": "fork", "assets_create_suggestion": "suggestion-create",
		"assets_withdraw_suggestion": "suggestion-withdraw", "assets_reject_suggestion": "suggestion-reject",
		"assets_adopt_suggestion": "suggestion-adopt", "assets_create_comment": "comment-create"}[name]
}

func Collaboration(ctx context.Context, client *transport.Client, token, name string, data json.RawMessage) (map[string]any, error) {
	action := CollaborationAction(name)
	if action != "" {
		var in CollaborationInput
		if err := transport.DecodeInput(data, &in); err != nil {
			return nil, err
		}
		if _, err := resourcePath(ResourceInput{Type: in.Type, Owner: in.Owner, Slug: in.Slug}); err != nil {
			return nil, err
		}
		if !publicationIntent.MatchString(in.RequestKey) || !UUID.MatchString(in.ExpectedRevision) ||
			in.SuggestionID != "" && !UUID.MatchString(in.SuggestionID) ||
			in.ProposalContentItemID != "" && !UUID.MatchString(in.ProposalContentItemID) ||
			in.ProposalRevisionID != "" && !UUID.MatchString(in.ProposalRevisionID) ||
			in.DraftID != "" && !UUID.MatchString(in.DraftID) || len(in.LinkedAssets) > 128 {
			return nil, errors.New("invalid collaboration arguments")
		}
		var body map[string]any
		raw, _ := json.Marshal(in)
		json.Unmarshal(raw, &body)
		delete(body, "type")
		return transport.WriteResult(client.Do(ctx, "POST", "/api/v1/authoring/assets/"+in.Type+"/collaboration/"+action, token, in.RequestKey, body))
	}
	var path string
	switch name {
	case "assets_list_suggestions", "assets_list_comments":
		var in SuggestionsInput
		if err := transport.DecodeInput(data, &in); err != nil {
			return nil, err
		}
		base, err := resourcePath(in.ResourceInput)
		if err != nil {
			return nil, err
		}
		q, err := transport.PageQuery(in.Limit, 20, in.Cursor, nil)
		if err != nil {
			return nil, err
		}
		if !slices.Contains([]string{"", "open", "withdrawn", "rejected", "adopted"}, in.Status) || name == "assets_list_comments" && in.Status != "" || in.Revision != "" {
			return nil, errors.New("invalid list arguments")
		}
		if in.Status != "" {
			q.Set("status", in.Status)
		}
		suffix := "/suggestions"
		if name == "assets_list_comments" {
			suffix = "/comments"
		}
		path = base + suffix + "?" + q.Encode()
	case "assets_get_suggestion":
		var in SuggestionInput
		if err := transport.DecodeInput(data, &in); err != nil {
			return nil, err
		}
		base, err := resourcePath(in.ResourceInput)
		if err != nil {
			return nil, err
		}
		if !UUID.MatchString(in.SuggestionID) || in.Revision != "" {
			return nil, errors.New("invalid suggestion address")
		}
		path = base + "/suggestions/" + in.SuggestionID
		if in.Side != "" {
			if in.Side != "base" && in.Side != "proposal" {
				return nil, errors.New("invalid suggestion side")
			}
			q, err := transport.PageQuery(nil, 20, in.Cursor, in.BodyLimit)
			if err != nil {
				return nil, err
			}
			q.Set("side", in.Side)
			path += "/content?" + q.Encode()
		} else if in.Cursor != "" || in.BodyLimit != nil {
			return nil, errors.New("content side required")
		}
	case "assets_list_notifications", "assets_get_inbox":
		var in NotificationsInput
		if err := transport.DecodeInput(data, &in); err != nil {
			return nil, err
		}
		if !slices.Contains([]string{"", "all", "character", "worldbook"}, in.Type) {
			return nil, errors.New("invalid inbox type")
		}
		q, err := transport.PageQuery(in.Limit, 20, in.Cursor, nil)
		if err != nil {
			return nil, err
		}
		if name == "assets_get_inbox" && (in.Limit != nil || in.Cursor != "") {
			return nil, errors.New("inbox does not accept pagination")
		}
		if in.Type != "" {
			q.Set("type", in.Type)
		}
		path = "/api/v1/reading/assets/notifications?"
		if name == "assets_get_inbox" {
			path = "/api/v1/reading/assets/inbox?"
		}
		path += q.Encode()
	case "assets_get_my_stats":
		var in StatsInput
		if err := transport.DecodeInput(data, &in); err != nil {
			return nil, err
		}
		if in.Type != "character" && in.Type != "worldbook" {
			return nil, errors.New("invalid stats type")
		}
		path = "/api/v1/reading/assets/stats?type=" + url.QueryEscape(in.Type)
	default:
		return nil, errors.New("unknown collaboration operation")
	}
	return transport.ReadingResult(client.Request(ctx, "GET", path, token, nil))
}

func IsCollaboration(name string) bool {
	return CollaborationAction(name) != "" || slices.Contains([]string{"assets_list_suggestions", "assets_get_suggestion", "assets_list_comments", "assets_get_my_stats", "assets_list_notifications", "assets_get_inbox"}, name)
}
