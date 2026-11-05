// Package assets is the shared bounded formal-resource HTTP SDK.
package assets

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"

	"starry-cli/pkg/transport"
)

type SearchInput struct {
	Type    string   `json:"type,omitempty" jsonschema:"character, worldbook or all; all returns only granted types"`
	Keyword string   `json:"keyword,omitempty" jsonschema:"Optional metadata keyword, up to 128 Unicode scalars"`
	Tags    []string `json:"tags,omitempty" jsonschema:"All required tags, at most ten"`
	Author  string   `json:"author,omitempty" jsonschema:"Exact owner user ID"`
	Limit   *int     `json:"limit,omitempty" jsonschema:"1-20, default 10"`
	Cursor  string   `json:"cursor,omitempty" jsonschema:"Repeat the same filters with this opaque cursor"`
}

type ResourceInput struct {
	Type     string `json:"type" jsonschema:"character or worldbook"`
	Owner    string `json:"owner" jsonschema:"Owner user ID, not a resource UUID"`
	Slug     string `json:"slug" jsonschema:"Resource slug"`
	Revision string `json:"revision,omitempty" jsonschema:"Canonical lowercase revision UUID; omit for current head"`
}

type PackageInput struct {
	ResourceInput
	Profile      string `json:"profile,omitempty" jsonschema:"basic_package (default) or full_package; full requires an extra type scope"`
	Group        string `json:"group,omitempty" jsonschema:"all, basic, identity, behavior, advanced, media, links, entries, entry, mediaFiles or delivery. Read all selected fields before treating content as complete."`
	EntryOrdinal int    `json:"entryOrdinal,omitempty" jsonschema:"1-based ordinal from the fixed revision's entry list; required with group=entry"`
	FileRefID    string `json:"fileRefId,omitempty" jsonschema:"Exact file-ref UUID from group=mediaFiles; required with group=delivery"`
	Limit        *int   `json:"limit,omitempty" jsonschema:"Entry page 1-50; media page 1-20"`
	BodyLimit    *int   `json:"bodyLimit,omitempty" jsonschema:"1000-12000 Unicode scalars, default 6000"`
	Cursor       string `json:"cursor,omitempty" jsonschema:"Continuation cursor; repeat all original arguments"`
}

type HistoryInput struct {
	ResourceInput
	Limit  *int   `json:"limit,omitempty" jsonschema:"1-20, default 10"`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque continuation"`
}

type DiffInput struct {
	ResourceInput
	From      string `json:"from,omitempty" jsonschema:"Lowercase revision UUID; omitted uses previous revision"`
	To        string `json:"to" jsonschema:"Required lowercase revision UUID"`
	BodyLimit *int   `json:"bodyLimit,omitempty" jsonschema:"1000-12000 Unicode scalars"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"Repeat from/to and all other arguments"`
}

var UUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var userID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var slugID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

func resourcePath(in ResourceInput) (string, error) {
	if !slices.Contains([]string{"character", "worldbook"}, in.Type) || !userID.MatchString(in.Owner) || !slugID.MatchString(in.Slug) || (in.Revision != "" && !UUID.MatchString(in.Revision)) {
		return "", errors.New("invalid resource address")
	}
	return "/api/v1/reading/assets/" + in.Type + "/" + url.PathEscape(in.Owner) + "/" + url.PathEscape(in.Slug), nil
}

func SearchPath(in SearchInput) (string, error) {
	q, err := transport.PageQuery(in.Limit, 20, in.Cursor, nil)
	if err != nil {
		return "", err
	}
	if in.Type == "" {
		in.Type = "all"
	}
	if !slices.Contains([]string{"all", "character", "worldbook"}, in.Type) || !transport.ValidText(in.Keyword, 128) || len(in.Tags) > 10 || (in.Author != "" && !userID.MatchString(in.Author)) {
		return "", errors.New("invalid search arguments")
	}
	for _, tag := range in.Tags {
		if !transport.ValidText(tag, 64) {
			return "", errors.New("invalid tag")
		}
		q.Add("tag", tag)
	}
	q.Set("type", in.Type)
	if in.Keyword != "" {
		q.Set("keyword", in.Keyword)
	}
	if in.Author != "" {
		q.Set("author", in.Author)
	}
	return "/api/v1/reading/assets?" + q.Encode(), nil
}

func PackagePath(in PackageInput) (string, error) {
	path, err := resourcePath(in.ResourceInput)
	if err != nil {
		return "", err
	}
	maximum := 20
	if in.Group == "entries" {
		maximum = 50
	}
	q, err := transport.PageQuery(in.Limit, maximum, in.Cursor, in.BodyLimit)
	if err != nil {
		return "", err
	}
	if !slices.Contains([]string{"", "basic_package", "full_package"}, in.Profile) || !slices.Contains([]string{"", "all", "basic", "identity", "behavior", "advanced", "media", "links", "entries", "entry", "mediaFiles", "delivery"}, in.Group) {
		return "", errors.New("invalid package selection")
	}
	suffix := "/content"
	switch in.Group {
	case "entries":
		if in.Type != "worldbook" {
			return "", errors.New("worldbook required")
		}
		suffix = "/entries"
	case "entry":
		if in.Type != "worldbook" || in.EntryOrdinal < 1 {
			return "", errors.New("invalid entry ordinal")
		}
		q.Set("entryOrdinal", strconv.Itoa(in.EntryOrdinal))
	case "mediaFiles":
		suffix = "/media"
	case "delivery":
		if !UUID.MatchString(in.FileRefID) || in.Revision == "" {
			return "", errors.New("fixed revision and fileRefId required")
		}
		suffix = "/media/" + in.FileRefID
	}
	if in.Group != "entry" && in.EntryOrdinal != 0 || in.Group != "delivery" && in.FileRefID != "" {
		return "", errors.New("invalid package arguments")
	}
	if in.Revision != "" {
		q.Set("revision", in.Revision)
	}
	if in.Profile != "" {
		q.Set("profile", in.Profile)
	}
	if in.Group != "" {
		q.Set("group", in.Group)
	}
	return path + suffix + "?" + q.Encode(), nil
}

func Call(ctx context.Context, client *transport.Client, token, name string, data json.RawMessage) (map[string]any, error) {
	var path string
	var err error
	switch name {
	case "assets_search":
		var in SearchInput
		err = transport.DecodeInput(data, &in)
		if err == nil {
			path, err = SearchPath(in)
		}
	case "assets_get_revision":
		var in ResourceInput
		err = transport.DecodeInput(data, &in)
		if err == nil {
			path, err = resourcePath(in)
			if in.Revision != "" {
				path += "?revision=" + url.QueryEscape(in.Revision)
			}
		}
	case "assets_get_package":
		var in PackageInput
		err = transport.DecodeInput(data, &in)
		if err == nil {
			path, err = PackagePath(in)
		}
	case "assets_list_revisions":
		var in HistoryInput
		err = transport.DecodeInput(data, &in)
		if err == nil {
			path, err = resourcePath(in.ResourceInput)
			if err == nil {
				var q url.Values
				q, err = transport.PageQuery(in.Limit, 20, in.Cursor, nil)
				if err != nil {
					return nil, err
				}
				if in.Revision != "" {
					q.Set("revision", in.Revision)
				}
				path += "/history?" + q.Encode()
			}
		}
	case "assets_diff":
		var in DiffInput
		err = transport.DecodeInput(data, &in)
		if err == nil {
			path, err = resourcePath(in.ResourceInput)
			if !UUID.MatchString(in.To) || in.From != "" && !UUID.MatchString(in.From) {
				err = errors.New("invalid revision pair")
			}
			if err == nil {
				var q url.Values
				q, err = transport.PageQuery(nil, 20, in.Cursor, in.BodyLimit)
				if err == nil {
					q.Set("to", in.To)
					if in.From != "" {
						q.Set("from", in.From)
					}
					path += "/difference?" + q.Encode()
				}
			}
		}
	default:
		return nil, errors.New("unknown asset operation")
	}
	if err != nil {
		return nil, err
	}
	return transport.ReadingResult(client.Request(ctx, "GET", path, token, nil))
}
