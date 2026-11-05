package assets

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"starry-cli/pkg/transport"
)

type PublicationInput struct {
	Type                string `json:"type" jsonschema:"character or worldbook"`
	RequestKey          string `json:"requestKey" jsonschema:"Stable intent key; preserve after outcome_unknown"`
	DraftID             string `json:"draftId" jsonschema:"Owned workspace UUID"`
	Slug                string `json:"slug" jsonschema:"Target publish slug"`
	PublishAs           string `json:"publishAs" jsonschema:"Explicit owner or ai; separate signing permission required"`
	AIIdentityID        string `json:"aiIdentityId,omitempty" jsonschema:"Required approved active identity for ai; omitted for owner"`
	PreviewID           string `json:"previewId,omitempty" jsonschema:"Publish only: operationId from preview"`
	Snapshot            string `json:"snapshot,omitempty" jsonschema:"Publish only: exact preview snapshot hash; all preview input fields retained"`
	Visibility          string `json:"visibility,omitempty" jsonschema:"First publish only: private, unlisted or public; later changes use policy"`
	PackageAccessPolicy string `json:"packageAccessPolicy,omitempty" jsonschema:"First publish only: package policy; ordinary republish preserves current policy"`
}
type PolicyPatch struct {
	Visibility          *string  `json:"visibility,omitempty"`
	DerivativePolicy    *string  `json:"derivative_policy,omitempty"`
	PackageAccessPolicy *string  `json:"package_access_policy,omitempty"`
	Confirmations       []string `json:"confirmations,omitempty" jsonschema:"Explicit acknowledgements required by current policy change; never add automatically"`
}
type PolicyInput struct {
	Type       string      `json:"type" jsonschema:"character or worldbook"`
	RequestKey string      `json:"requestKey" jsonschema:"Stable operation intent key"`
	Slug       string      `json:"slug" jsonschema:"Owned formal asset slug"`
	IfMatch    string      `json:"ifMatch" jsonschema:"Required current policy_N version"`
	Patch      PolicyPatch `json:"patch" jsonschema:"Only explicit requested policy changes"`
}

var publicationIntent = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func Publication(ctx context.Context, client *transport.Client, token, name string, data json.RawMessage) (map[string]any, error) {
	action := ""
	switch name {
	case "assets_preview_publish":
		action = "preview"
	case "assets_publish":
		action = "publish"
	case "assets_update_policy":
		action = "policy"
	default:
		return nil, errors.New("unknown publication action")
	}
	var body map[string]any
	kind, key := "", ""
	if action == "policy" {
		var in PolicyInput
		if err := transport.DecodeInput(data, &in); err != nil {
			return nil, err
		}
		if !publicationIntent.MatchString(in.RequestKey) || !slugID.MatchString(in.Slug) || in.IfMatch == "" {
			return nil, errors.New("invalid policy input")
		}
		kind, key = in.Type, in.RequestKey
		raw, _ := json.Marshal(in)
		json.Unmarshal(raw, &body)
	} else {
		var in PublicationInput
		if err := transport.DecodeInput(data, &in); err != nil {
			return nil, err
		}
		if !publicationIntent.MatchString(in.RequestKey) || !UUID.MatchString(in.DraftID) || !slugID.MatchString(in.Slug) ||
			in.PublishAs != "owner" && in.PublishAs != "ai" || in.PublishAs == "owner" && in.AIIdentityID != "" ||
			in.PublishAs == "ai" && in.AIIdentityID == "" || action == "preview" && (in.PreviewID != "" || in.Snapshot != "") ||
			action == "publish" && (!UUID.MatchString(in.PreviewID) || len(in.Snapshot) != 64) {
			return nil, errors.New("invalid publication input")
		}
		kind, key = in.Type, in.RequestKey
		raw, _ := json.Marshal(in)
		json.Unmarshal(raw, &body)
	}
	if kind != "character" && kind != "worldbook" {
		return nil, errors.New("invalid resource type")
	}
	delete(body, "type")
	return transport.WriteResult(client.Do(ctx, "POST", "/api/v1/authoring/assets/"+url.PathEscape(kind)+"/"+action, token, key, body))
}

func IsPublication(name string) bool {
	return strings.Contains("|assets_preview_publish|assets_publish|assets_update_policy|", "|"+name+"|")
}
