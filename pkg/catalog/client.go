// Package catalog reads the owner's workspace through bounded HTTP projections.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"

	"starry-cli/pkg/assets"
	"starry-cli/pkg/transport"
)

type ListInput struct {
	Limit  *int   `json:"limit,omitempty" jsonschema:"1-20, default 10"`
	Cursor string `json:"cursor,omitempty" jsonschema:"Repeat the same filters with this opaque cursor"`
}
type GetInput struct {
	ID        string `json:"id" jsonschema:"Canonical lowercase resource UUID"`
	Group     string `json:"group,omitempty" jsonschema:"all (default), basic, identity, behavior, advanced, media or links"`
	MediaRole string `json:"mediaRole,omitempty" jsonschema:"avatar, cover, illustration or background; resolves a temporary owner-only media URL instead of content"`
	BodyLimit *int   `json:"bodyLimit,omitempty" jsonschema:"1000-12000 Unicode scalars, default 6000"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"Opaque field continuation; content_changed requires a fresh read"`
}
type EntriesInput struct {
	BookID string `json:"bookId" jsonschema:"Owned worldbook UUID; never fetch entries without a parent"`
	Limit  *int   `json:"limit,omitempty" jsonschema:"1-50, default 20"`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque cursor bound to the book's aggregate version"`
}
type EntryInput struct {
	BookID    string `json:"bookId" jsonschema:"Owned worldbook UUID"`
	EntryID   string `json:"entryId" jsonschema:"Entry UUID belonging to this book"`
	BodyLimit *int   `json:"bodyLimit,omitempty" jsonschema:"1000-12000 Unicode scalars"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"Opaque field continuation"`
}
type BindingsInput struct {
	CharacterID string `json:"characterId" jsonschema:"Owned character UUID"`
	Limit       *int   `json:"limit,omitempty" jsonschema:"1-50, default 20"`
	Cursor      string `json:"cursor,omitempty" jsonschema:"Opaque cursor bound to the character's aggregate version"`
}

func Call(ctx context.Context, client *transport.Client, token, name string, data json.RawMessage) (map[string]any, error) {
	if WriteActions[name] != "" {
		return Write(ctx, client, token, name, data)
	}
	var path string
	var q url.Values
	var err error
	base := "/api/v1/reading/catalog/"
	switch name {
	case "catalog_list_characters", "catalog_list_worldbooks":
		var in ListInput
		err = transport.DecodeInput(data, &in)
		if err == nil {
			q, err = transport.PageQuery(in.Limit, 20, in.Cursor, nil)
		}
		path = base + "characters"
		if name == "catalog_list_worldbooks" {
			path = base + "worldbooks"
		}
	case "catalog_get_character", "catalog_get_worldbook":
		var in GetInput
		err = transport.DecodeInput(data, &in)
		if err == nil && !assets.UUID.MatchString(in.ID) {
			err = errors.New("invalid resource UUID")
		}
		if err == nil {
			q, err = transport.PageQuery(nil, 20, in.Cursor, in.BodyLimit)
			if err == nil && in.Group != "" {
				q.Set("group", in.Group)
			}
		}
		path = base + "characters/" + in.ID
		if name == "catalog_get_worldbook" {
			path = base + "worldbooks/" + in.ID
		}
		if in.MediaRole != "" {
			switch in.MediaRole {
			case "avatar", "cover", "illustration", "background":
			default:
				return nil, errors.New("invalid mediaRole")
			}
			if in.Group != "" || in.Cursor != "" || in.BodyLimit != nil {
				return nil, errors.New("media resolution cannot continue content")
			}
			path += "/media/" + in.MediaRole
		}
	case "catalog_list_worldbook_entries":
		var in EntriesInput
		err = transport.DecodeInput(data, &in)
		if err == nil && !assets.UUID.MatchString(in.BookID) {
			err = errors.New("invalid book UUID")
		}
		if err == nil {
			q, err = transport.PageQuery(in.Limit, 50, in.Cursor, nil)
		}
		path = base + "worldbooks/" + in.BookID + "/entries"
	case "catalog_get_worldbook_entry":
		var in EntryInput
		err = transport.DecodeInput(data, &in)
		if err == nil && (!assets.UUID.MatchString(in.BookID) || !assets.UUID.MatchString(in.EntryID)) {
			err = errors.New("invalid book or entry UUID")
		}
		if err == nil {
			q, err = transport.PageQuery(nil, 50, in.Cursor, in.BodyLimit)
		}
		path = base + "worldbooks/" + in.BookID + "/entries/" + in.EntryID
	case "catalog_list_character_bindings":
		var in BindingsInput
		err = transport.DecodeInput(data, &in)
		if err == nil && !assets.UUID.MatchString(in.CharacterID) {
			err = errors.New("invalid character UUID")
		}
		if err == nil {
			q, err = transport.PageQuery(in.Limit, 50, in.Cursor, nil)
		}
		path = base + "characters/" + in.CharacterID + "/bindings"
	default:
		return nil, errors.New("unknown catalog operation")
	}
	if err != nil {
		return nil, err
	}
	return transport.ReadingResult(client.Request(ctx, "GET", path+"?"+q.Encode(), token, nil))
}
