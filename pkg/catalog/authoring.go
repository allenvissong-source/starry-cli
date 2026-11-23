package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"

	"starry-cli/pkg/assets"
	"starry-cli/pkg/transport"
)

type Change struct {
	Action          string         `json:"action" jsonschema:"create, update or delete; every change is atomic with the entire batch"`
	ID              string         `json:"id" jsonschema:"Stable canonical UUID; a batch cannot repeat an ID"`
	ExpectedVersion int64          `json:"expectedVersion,omitempty" jsonschema:"Required for update/delete; zero for create"`
	Fields          map[string]any `json:"fields,omitempty" jsonschema:"Canonical entry fields; omitted preserved, null explicitly cleared where supported"`
}

type WriteInput struct {
	RequestKey             string         `json:"requestKey" jsonschema:"Stable intent key; retain the same key and complete input after unknown outcome"`
	ID                     string         `json:"id,omitempty" jsonschema:"Stable canonical lowercase UUID; client supplies it when creating"`
	ParentID               string         `json:"parentId,omitempty" jsonschema:"Required owned worldbook for entries, owned character for bindings"`
	ExpectedVersion        int64          `json:"expectedVersion,omitempty" jsonschema:"Required entity version for update/delete/media; zero for create"`
	ExpectedReadingVersion string         `json:"expectedReadingVersion,omitempty" jsonschema:"Required aggregate readingVersion for existing roots or all child writes"`
	Fields                 map[string]any `json:"fields,omitempty" jsonschema:"Whitelist canonical Catalog fields; unknown or trusted identity fields rejected; omitted fields preserved"`
	Changes                []Change       `json:"changes,omitempty" jsonschema:"1-50 entry changes for apply; atomic, each existing entry needs expectedVersion"`
	MediaRole              string         `json:"mediaRole,omitempty" jsonschema:"Media only: avatar, cover, illustration, background"`
	MediaAssetID           *string        `json:"mediaAssetId,omitempty" jsonschema:"Media only: owned registered image UUID; null clears the selected slot"`
	AcquisitionOrigin      map[string]any `json:"acquisitionOrigin,omitempty" jsonschema:"Root creation only: package working-copy source with owner_user_id, resource_type and resource_id. The backend requires an existing source of the same owner and type and inherits its trusted lineage."`
}

var WriteActions = map[string]string{
	"catalog_create_character": "character.create", "catalog_update_character": "character.update", "catalog_delete_character": "character.delete",
	"catalog_create_worldbook": "worldbook.create", "catalog_update_worldbook": "worldbook.update", "catalog_delete_worldbook": "worldbook.delete",
	"catalog_create_worldbook_entry": "entry.create", "catalog_update_worldbook_entry": "entry.update", "catalog_delete_worldbook_entry": "entry.delete",
	"catalog_apply_worldbook_entry_changes": "entry.apply",
	"catalog_bind_worldbook":                "binding.create", "catalog_unbind_worldbook": "binding.delete",
	"catalog_set_character_media": "character.media", "catalog_set_worldbook_media": "worldbook.media",
	"catalog_create_skill": "skill.create", "catalog_update_skill": "skill.update", "catalog_delete_skill": "skill.delete",
	"catalog_create_skill_binding": "skill_binding.create", "catalog_update_skill_binding": "skill_binding.update",
	"catalog_delete_skill_binding": "skill_binding.delete", "catalog_apply_character_skills": "skill_binding.apply",
	"catalog_create_collection": "collection.create", "catalog_update_collection": "collection.update", "catalog_delete_collection": "collection.delete",
	"catalog_apply_collection_items": "collection_item.apply",
}
var intentKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func InputLimit(name string) int {
	return transport.RequestBodyLimit("POST", "/api/v1/authoring/catalog/"+WriteActions[name])
}

func Write(ctx context.Context, client *transport.Client, token, name string, data json.RawMessage) (map[string]any, error) {
	action := WriteActions[name]
	if action == "" {
		return nil, errors.New("unknown authoring action")
	}
	var input WriteInput
	if err := transport.DecodeInputBounded(data, &input, InputLimit(name)); err != nil {
		return nil, err
	}
	if !intentKey.MatchString(input.RequestKey) || input.ID != "" && !assets.UUID.MatchString(input.ID) ||
		input.ParentID != "" && !assets.UUID.MatchString(input.ParentID) {
		return nil, errors.New("invalid intent key or resource UUID")
	}
	return transport.WriteResult(client.Do(ctx, "POST", "/api/v1/authoring/catalog/"+url.PathEscape(action), token, input.RequestKey, input))
}

type OperationInput struct {
	OperationID string `json:"operationId,omitempty" jsonschema:"Operation UUID belonging to this connection"`
	Action      string `json:"action,omitempty" jsonschema:"Exact authoring action, for example character.update or assets.character.publish"`
	RequestKey  string `json:"requestKey,omitempty" jsonschema:"Original intent key; not found does not establish that a write did not commit"`
}

func Operation(ctx context.Context, client *transport.Client, token string, data json.RawMessage) (map[string]any, error) {
	var input OperationInput
	if err := transport.DecodeInput(data, &input); err != nil {
		return nil, err
	}
	path := "/api/v1/authoring/operations"
	if input.OperationID != "" {
		if !assets.UUID.MatchString(input.OperationID) || input.Action != "" || input.RequestKey != "" {
			return nil, errors.New("invalid operation selector")
		}
		path += "/" + input.OperationID
	} else {
		if !intentKey.MatchString(input.RequestKey) || input.Action == "" || len(input.Action) > 80 {
			return nil, errors.New("operation requires action and original requestKey")
		}
		path += "?" + url.Values{"action": {input.Action}, "requestKey": {input.RequestKey}}.Encode()
	}
	return transport.ReadingResult(client.Request(ctx, "GET", path, token, nil))
}
