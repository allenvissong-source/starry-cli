package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"unicode/utf8"
)

func DecodeInput(data json.RawMessage, out any) error {
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	if len(data) > 64<<10 || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return errors.New("invalid JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid command input")
	}
	return nil
}

func PageQuery(limit *int, maximum int, cursor string, bodyLimit *int) (url.Values, error) {
	if limit != nil && (*limit < 1 || *limit > maximum) {
		return nil, errors.New("invalid limit")
	}
	if bodyLimit != nil && (*bodyLimit < 1000 || *bodyLimit > 12000) {
		return nil, errors.New("invalid bodyLimit")
	}
	if len(cursor) > 8192 {
		return nil, errors.New("invalid cursor")
	}
	for _, ch := range cursor {
		if ch < 33 || ch > 126 {
			return nil, errors.New("invalid cursor")
		}
	}
	q := url.Values{}
	if limit != nil {
		q.Set("limit", strconv.Itoa(*limit))
	}
	if bodyLimit != nil {
		q.Set("bodyLimit", strconv.Itoa(*bodyLimit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return q, nil
}

func ValidText(value string, maximum int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= maximum
}

func ReadingResult(raw json.RawMessage, err error) (map[string]any, error) {
	if err != nil {
		return nil, err
	}
	var value struct {
		Page *struct {
			HasMore    bool   `json:"hasMore"`
			NextCursor string `json:"nextCursor"`
			EndReason  string `json:"endReason"`
		} `json:"page"`
	}
	if len(raw) > 1<<20 || json.Unmarshal(raw, &value) != nil {
		return nil, NewAPIError(503, "upstream_unavailable")
	}
	if value.Page != nil && (value.Page.HasMore != (value.Page.NextCursor != "") || (value.Page.EndReason != "limit" && value.Page.EndReason != "complete")) {
		return nil, NewAPIError(503, "invalid_page")
	}
	return map[string]any{"data": raw}, nil
}
