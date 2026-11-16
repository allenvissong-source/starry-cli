package community

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestPrincipalUIDPreservesPrecisionAndRejectsOldIdentifiers(t *testing.T) {
	for _, id := range []string{"0", "1000", "9007199254740993", "9223372036854775807"} {
		data, _ := json.Marshal(map[string]any{"token": "delegate", "principal": map[string]string{"ownerUserId": id}})
		result, err := decodeExchange(data, nil)
		if err != nil || result.Principal.OwnerUserID != id {
			t.Fatalf("UID round trip=%q err=%v", result.Principal.OwnerUserID, err)
		}
	}
	for _, raw := range []string{`"user_old"`, `"01"`, `"9223372036854775808"`, `9007199254740993`} {
		if _, err := decodeExchange(json.RawMessage(`{"token":"delegate","principal":{"ownerUserId":`+raw+`}}`), nil); err == nil {
			t.Fatalf("invalid wire UID accepted: %s", raw)
		}
	}
}

func TestAuthorFilterUsesCanonicalUIDOrMe(t *testing.T) {
	for _, id := range []string{"0", "999", "1000", "9007199254740993", "9223372036854775807", "me"} {
		path, err := listPath(ListInput{AuthorID: id, Cursor: "next"})
		uri, parseErr := url.Parse(path)
		if err != nil || parseErr != nil || uri.Query().Get("authorId") != id || uri.Query().Get("cursor") != "next" {
			t.Fatalf("author filter changed: %q %s %v", id, path, err)
		}
	}
	for _, id := range []string{"user_old", "owner:1", "01", "+1000", "-1", "1e3", "１０００", "9223372036854775808", " me "} {
		if _, err := listPath(ListInput{AuthorID: id}); err == nil {
			t.Fatalf("invalid author UID accepted: %s", id)
		}
	}
}
