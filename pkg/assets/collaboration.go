package assets

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"

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
	Type   string `json:"type,omitempty" jsonschema:"character, worldbook, skill, collection or all; all includes only granted types"`
	Limit  *int   `json:"limit,omitempty" jsonschema:"1-20, default 10"`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque continuation; reading never marks human messages read"`
}

type StatsInput struct {
	Type string `json:"type" jsonschema:"character, worldbook, skill or collection; statistics cover only the owner's resources"`
}

type LinkedAssetChoice struct {
	ID   string `json:"id" jsonschema:"Exact linked_assets ID from the source manifest"`
	Mode string `json:"mode" jsonschema:"reference, fork or omit; fork currently preserves the immutable reference"`
}

type SkillAcquisitionBindingRef struct {
	ID              string `json:"id" jsonschema:"Owned existing character Skill binding UUID"`
	ExpectedVersion int64  `json:"expectedVersion" jsonschema:"Exact current Catalog entity_version"`
}

type SkillAcquisitionUpdate struct {
	SourceSkillID   string                       `json:"sourceSkillId" jsonschema:"Previously acquired Skill UUID from this same formal source; preserved"`
	ExpectedVersion int64                        `json:"expectedVersion" jsonschema:"Exact current entity_version of that owned Skill"`
	Bindings        []SkillAcquisitionBindingRef `json:"bindings" jsonschema:"Up to 50 explicitly selected bindings to replace atomically; empty keeps every character on its confirmed version"`
}

type CollaborationInput struct {
	Type                   string                  `json:"type" jsonschema:"character, worldbook, skill or collection"`
	RequestKey             string                  `json:"requestKey" jsonschema:"Stable intent key; retain exact input on ambiguous failure"`
	Owner                  string                  `json:"owner" jsonschema:"Base resource owner"`
	Slug                   string                  `json:"slug" jsonschema:"Base resource slug"`
	ExpectedRevision       string                  `json:"expectedRevision" jsonschema:"Required current base head UUID"`
	SuggestionID           string                  `json:"suggestionId,omitempty"`
	ProposalContentItemID  string                  `json:"proposalContentItemId,omitempty" jsonschema:"Own fork repository ID, not draft ID"`
	ProposalRevisionID     string                  `json:"proposalRevisionId,omitempty" jsonschema:"Fixed published proposal revision"`
	Title                  string                  `json:"title,omitempty"`
	Body                   string                  `json:"body,omitempty"`
	LinkedAssets           []LinkedAssetChoice     `json:"linkedAssets,omitempty"`
	PublishAs              string                  `json:"publishAs,omitempty" jsonschema:"Adoption requires owner or ai; comment requires owner"`
	AIIdentityID           string                  `json:"aiIdentityId,omitempty"`
	DraftID                string                  `json:"draftId,omitempty" jsonschema:"Fork only: optional prepared unpublished owned workspace; fork attaches lineage but does not import content"`
	ExpectedDraftVersion   int64                   `json:"expectedDraftVersion,omitempty"`
	ExpectedReadingVersion string                  `json:"expectedReadingVersion,omitempty"`
	Mode                   string                  `json:"mode,omitempty" jsonschema:"Acquisition only: read_only permits local use of readable no_copy content; editable additionally requires fork permission. Default editable. Neither grants republication."`
	Selection              *[]string               `json:"selection,omitempty" jsonschema:"Acquisition only: exact direct member addresses type/owner/slug/revision from the root package. Omit for all; empty array selects none. Every selected member retains all dependencies."`
	Strategy               string                  `json:"strategy,omitempty" jsonschema:"Acquisition only: reuse unchanged same-version resources (default), or copy to separate owned resources. Existing edited or other-version content is never overwritten."`
	PlanHash               string                  `json:"planHash,omitempty" jsonschema:"Acquisition requires the plan_hash from assets_plan_acquisition for the exact mode, selection and strategy. A plan_changed response requires review of a new plan."`
	SkillUpdate            *SkillAcquisitionUpdate `json:"skillUpdate,omitempty" jsonschema:"Skill acquisition only: preserve the old Skill and update only these selected character bindings in the same transaction. User overrides are preserved; author recommendations follow the new snapshot."`
}

type AcquisitionPlanInput struct {
	Type        string                  `json:"type" jsonschema:"character, worldbook, skill or collection"`
	Owner       string                  `json:"owner"`
	Slug        string                  `json:"slug"`
	Revision    string                  `json:"revision" jsonschema:"Exact fixed root revision"`
	Mode        string                  `json:"mode,omitempty" jsonschema:"read_only or editable (default); neither grants republication"`
	Selection   *[]string               `json:"selection,omitempty" jsonschema:"Exact direct member addresses type/owner/slug/revision; omitted selects all, empty selects none"`
	Strategy    string                  `json:"strategy,omitempty" jsonschema:"reuse (default) or copy; old and locally edited resources are preserved"`
	SkillUpdate *SkillAcquisitionUpdate `json:"skillUpdate,omitempty" jsonschema:"Skill only: exact old acquired Skill and selected binding versions; reflected in binding_updates and protected by plan_hash"`
}

type AcquiredSkillUpdateSourceInput struct {
	ID string `json:"id" jsonschema:"Owned previously acquired Skill UUID; source is resolved from protected server lineage"`
}

func CollaborationAction(name string) string {
	return map[string]string{"assets_fork": "fork", "assets_acquire": "acquire", "assets_create_suggestion": "suggestion-create",
		"assets_withdraw_suggestion": "suggestion-withdraw", "assets_reject_suggestion": "suggestion-reject",
		"assets_adopt_suggestion": "suggestion-adopt", "assets_create_comment": "comment-create"}[name]
}

func Collaboration(ctx context.Context, client *transport.Client, token, name string, data json.RawMessage) (map[string]any, error) {
	if name == "assets_get_skill_update_source" {
		var in AcquiredSkillUpdateSourceInput
		if err := transport.DecodeInput(data, &in); err != nil {
			return nil, err
		}
		if !UUID.MatchString(in.ID) {
			return nil, errors.New("invalid acquired Skill UUID")
		}
		return transport.ReadingResult(client.Request(ctx, "POST", "/api/v1/authoring/assets/skill/update-source", token, in))
	}
	if name == "assets_plan_acquisition" {
		var in AcquisitionPlanInput
		if err := transport.DecodeInput(data, &in); err != nil {
			return nil, err
		}
		if _, err := resourcePath(ResourceInput{Type: in.Type, Owner: in.Owner, Slug: in.Slug}); err != nil {
			return nil, err
		}
		if !UUID.MatchString(in.Revision) || !validAcquisitionChoices(in.Mode, in.Strategy, in.Selection) ||
			!validSkillAcquisitionUpdate(in.Type, in.SkillUpdate) {
			return nil, errors.New("invalid acquisition plan")
		}
		body := map[string]any{"owner": in.Owner, "slug": in.Slug, "expectedRevision": in.Revision,
			"mode": in.Mode, "strategy": in.Strategy}
		if in.Selection != nil {
			body["selection"] = *in.Selection
		}
		if in.SkillUpdate != nil {
			body["skillUpdate"] = in.SkillUpdate
		}
		return transport.ReadingResult(client.Request(ctx, "POST", "/api/v1/authoring/assets/"+in.Type+"/acquisition-plan", token, body))
	}
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
		if action == "acquire" && (in.SuggestionID != "" || in.ProposalContentItemID != "" ||
			in.ProposalRevisionID != "" || in.Title != "" || in.Body != "" || len(in.LinkedAssets) != 0 ||
			in.PublishAs != "" || in.AIIdentityID != "" || in.DraftID != "" ||
			in.ExpectedDraftVersion != 0 || in.ExpectedReadingVersion != "") {
			return nil, errors.New("acquire requires only type, owner, slug, expectedRevision and requestKey")
		}
		if action == "acquire" && (!validAcquisitionChoices(in.Mode, in.Strategy, in.Selection) || !MediaHash.MatchString(in.PlanHash) ||
			!validSkillAcquisitionUpdate(in.Type, in.SkillUpdate)) ||
			action != "acquire" && (in.Mode != "" || in.Strategy != "" || in.Selection != nil || in.PlanHash != "" || in.SkillUpdate != nil) {
			return nil, errors.New("acquisition requires exact choices and a confirmed plan hash")
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
		if in.Type != "" && in.Type != "all" && !IsResourceType(in.Type) {
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
		if !IsResourceType(in.Type) {
			return nil, errors.New("invalid stats type")
		}
		path = "/api/v1/reading/assets/stats?type=" + url.QueryEscape(in.Type)
	default:
		return nil, errors.New("unknown collaboration operation")
	}
	return transport.ReadingResult(client.Request(ctx, "GET", path, token, nil))
}

func validSkillAcquisitionUpdate(kind string, update *SkillAcquisitionUpdate) bool {
	if update == nil {
		return true
	}
	if kind != "skill" || !UUID.MatchString(update.SourceSkillID) || update.ExpectedVersion < 1 ||
		len(update.Bindings) > 50 || update.Bindings == nil {
		return false
	}
	seen := map[string]bool{}
	for _, binding := range update.Bindings {
		if !UUID.MatchString(binding.ID) || binding.ExpectedVersion < 1 || seen[binding.ID] {
			return false
		}
		seen[binding.ID] = true
	}
	return true
}

func IsCollaboration(name string) bool {
	return name == "assets_get_skill_update_source" || name == "assets_plan_acquisition" || CollaborationAction(name) != "" || slices.Contains([]string{"assets_list_suggestions", "assets_get_suggestion", "assets_list_comments", "assets_get_my_stats", "assets_list_notifications", "assets_get_inbox"}, name)
}

func validAcquisitionChoices(mode, strategy string, selection *[]string) bool {
	if mode != "" && mode != "read_only" && mode != "editable" ||
		strategy != "" && strategy != "reuse" && strategy != "copy" {
		return false
	}
	if selection == nil {
		return true
	}
	if len(*selection) > 128 {
		return false
	}
	seen := map[string]bool{}
	for _, key := range *selection {
		parts := strings.Split(key, "/")
		if seen[key] || len(parts) != 4 || !UUID.MatchString(parts[3]) {
			return false
		}
		seen[key] = true
		if parts[0] != "live2d" && !IsResourceType(parts[0]) || parts[0] == "collection" {
			return false
		}
		if parts[1] == "" || parts[2] == "" {
			return false
		}
	}
	return true
}
