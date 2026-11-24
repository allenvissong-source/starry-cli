package community

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/allenvissong-source/starry-cli/pkg/transport"
)

const (
	maxCursorLength = 4096
	maxListLimit    = 20
	maxTopicLimit   = 50
	maxCommentLimit = 10
	minBodyLimit    = 1000
	maxBodyLimit    = 12000
	maxKeywordRunes = 128
)

type ListInput struct {
	Kind     string `json:"kind,omitempty" jsonschema:"Post kind: dynamic, article, or all (default all)"`
	Source   string `json:"source,omitempty" jsonschema:"Sort source: latest, following, featured, recommended (default), or discover"`
	NodeID   string `json:"nodeId,omitempty" jsonschema:"Existing positive content node ID"`
	TopicID  string `json:"topicId,omitempty" jsonschema:"Existing positive topic ID; combined with nodeId as AND"`
	TagID    string `json:"tagId,omitempty" jsonschema:"Existing positive tag ID; combined with all other filters as AND"`
	AuthorID string `json:"authorId,omitempty" jsonschema:"Canonical decimal account UID string, or me for the authenticated owner"`
	Sort     string `json:"sort,omitempty" jsonschema:"new (default) or hot; hot requires source=latest"`
	Limit    *int   `json:"limit,omitempty" jsonschema:"Page size 1-20, default 10"`
	Cursor   string `json:"cursor,omitempty" jsonschema:"Opaque cursor from the previous result"`
}
type SearchInput struct {
	Keyword   string `json:"keyword" jsonschema:"Required keyword, at most 128 Unicode code points"`
	Kind      string `json:"kind,omitempty" jsonschema:"Post kind: dynamic, article, or all (default all)"`
	NodeID    string `json:"nodeId,omitempty" jsonschema:"Existing positive node ID; combined with keyword as AND"`
	TimeRange *int   `json:"timeRange,omitempty" jsonschema:"0 all (default), 1 day, 2 week, 3 month, 4 year"`
	Limit     *int   `json:"limit,omitempty" jsonschema:"Page size 1-20, default 10"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"Opaque cursor from the previous result"`
}
type TopicsInput struct {
	Limit  *int   `json:"limit,omitempty" jsonschema:"Page size 1-50, default 20"`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque cursor from the previous result"`
}
type PostInput struct {
	Kind            string `json:"kind" jsonschema:"Post kind: dynamic or article"`
	ID              string `json:"id" jsonschema:"Positive decimal post ID; keep it as a string"`
	BodyLimit       *int   `json:"bodyLimit,omitempty" jsonschema:"Body segment size in Unicode code points, 1000-12000, default 6000"`
	BodyCursor      string `json:"bodyCursor,omitempty" jsonschema:"Opaque body cursor returned with hasMore=true"`
	IncludeComments *bool  `json:"includeComments,omitempty" jsonschema:"Include a comment preview, default true. Must be false when continuing the body."`
	CommentLimit    *int   `json:"commentLimit,omitempty" jsonschema:"Comment preview size 1-10, default 5; only valid with includeComments"`
}
type CommentInput struct {
	Kind   string `json:"kind" jsonschema:"Target kind: dynamic, article, or comment (a top-level comment's replies)"`
	ID     string `json:"id" jsonschema:"Positive decimal target ID; keep it as a string"`
	Limit  *int   `json:"limit,omitempty" jsonschema:"Page size 1-20, default 10"`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque cursor from the previous result"`
	Sort   string `json:"sort,omitempty" jsonschema:"new (default) or hot; replies with kind=comment require new"`
}
type StatsInput struct {
	Subject      string `json:"subject,omitempty" jsonschema:"Whose totals: owner (default, whole account) or ai (one subordinate AI identity)"`
	AIIdentityID string `json:"aiIdentityId,omitempty" jsonschema:"Required with subject=ai; the AI identity must belong to the owner"`
}
type NotificationsInput struct {
	Status string `json:"status,omitempty" jsonschema:"Filter: unread, read, or all (default)"`
	Type   string `json:"type,omitempty" jsonschema:"Filter: topic_like, article_like, comment_like, topic_comment, article_comment, comment_reply, or follow"`
	Limit  *int   `json:"limit,omitempty" jsonschema:"Page size 1-20, default 10"`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque cursor from the previous result"`
}

func validCursor(raw string) bool {
	if len(raw) > maxCursorLength {
		return false
	}
	for _, ch := range raw {
		if ch < 33 || ch > 126 {
			return false
		}
	}
	return true
}

func validID(id string) bool {
	n, err := strconv.ParseInt(id, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == id
}

func checkLimit(limit *int, max int) error {
	if limit != nil && (*limit < 1 || *limit > max) {
		return fmt.Errorf("limit must be between 1 and %d", max)
	}
	return nil
}

func checkKind(kind string, allowAll bool) (string, error) {
	switch kind = strings.TrimSpace(kind); kind {
	case "":
		if allowAll {
			return "all", nil
		}
	case "dynamic", "article":
		return kind, nil
	case "all":
		if allowAll {
			return kind, nil
		}
	}
	return "", errors.New("invalid post kind")
}

func listPath(input ListInput) (string, error) {
	kind, err := checkKind(input.Kind, true)
	if err != nil {
		return "", err
	}
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = "recommended"
	}
	switch source {
	case "latest", "following", "featured", "recommended", "discover":
	default:
		return "", errors.New("invalid list source")
	}
	if err := checkLimit(input.Limit, maxListLimit); err != nil {
		return "", err
	}
	if !validCursor(input.Cursor) {
		return "", errors.New("invalid cursor")
	}
	q := url.Values{"kind": {kind}, "source": {source}}
	for _, item := range []struct{ key, value string }{{"nodeId", input.NodeID}, {"topicId", input.TopicID}, {"tagId", input.TagID}} {
		if item.value == "" {
			continue
		}
		if !validID(item.value) {
			return "", fmt.Errorf("invalid %s", item.key)
		}
		q.Set(item.key, item.value)
	}
	if input.AuthorID != "" {
		if !validAuthorID(input.AuthorID) {
			return "", errors.New("invalid authorId")
		}
		q.Set("authorId", input.AuthorID)
	}
	if input.Sort != "" {
		if (input.Sort != "new" && input.Sort != "hot") || (input.Sort == "hot" && source != "latest") {
			return "", errors.New("sort must be new, or hot with source=latest")
		}
		q.Set("sort", input.Sort)
	}
	if input.Limit != nil {
		q.Set("limit", strconv.Itoa(*input.Limit))
	}
	if input.Cursor != "" {
		q.Set("cursor", input.Cursor)
	}
	return "/api/v1/community/posts?" + q.Encode(), nil
}

func searchPath(input SearchInput) (string, error) {
	keyword := strings.TrimSpace(input.Keyword)
	if keyword == "" || !utf8.ValidString(keyword) || utf8.RuneCountInString(keyword) > maxKeywordRunes {
		return "", errors.New("keyword must be non-empty and at most 128 Unicode code points")
	}
	kind, err := checkKind(input.Kind, true)
	if err != nil {
		return "", err
	}
	if err := checkLimit(input.Limit, maxListLimit); err != nil {
		return "", err
	}
	if !validCursor(input.Cursor) {
		return "", errors.New("invalid cursor")
	}
	q := url.Values{"keyword": {keyword}, "kind": {kind}}
	if input.NodeID != "" {
		if !validID(input.NodeID) {
			return "", errors.New("invalid nodeId")
		}
		q.Set("nodeId", input.NodeID)
	}
	if input.TimeRange != nil {
		if *input.TimeRange < 0 || *input.TimeRange > 4 {
			return "", errors.New("timeRange must be between 0 and 4")
		}
		q.Set("timeRange", strconv.Itoa(*input.TimeRange))
	}
	if input.Limit != nil {
		q.Set("limit", strconv.Itoa(*input.Limit))
	}
	if input.Cursor != "" {
		q.Set("cursor", input.Cursor)
	}
	return "/api/v1/community/search?" + q.Encode(), nil
}

func topicsPath(input TopicsInput) (string, error) {
	if err := checkLimit(input.Limit, maxTopicLimit); err != nil {
		return "", err
	}
	if !validCursor(input.Cursor) {
		return "", errors.New("invalid cursor")
	}
	q := url.Values{}
	if input.Limit != nil {
		q.Set("limit", strconv.Itoa(*input.Limit))
	}
	if input.Cursor != "" {
		q.Set("cursor", input.Cursor)
	}
	return "/api/v1/community/topics?" + q.Encode(), nil
}

func postPath(input PostInput) (string, error) {
	kind, err := checkKind(input.Kind, false)
	if err != nil || !validID(input.ID) {
		return "", errors.New("invalid post arguments")
	}
	if input.BodyLimit != nil && (*input.BodyLimit < minBodyLimit || *input.BodyLimit > maxBodyLimit) {
		return "", fmt.Errorf("bodyLimit must be between %d and %d", minBodyLimit, maxBodyLimit)
	}
	if err := checkLimit(input.CommentLimit, maxCommentLimit); err != nil {
		return "", fmt.Errorf("commentLimit must be between 1 and %d", maxCommentLimit)
	}
	include := true
	if input.IncludeComments != nil {
		include = *input.IncludeComments
	}
	resuming := input.BodyCursor != ""
	if resuming {
		if !validCursor(input.BodyCursor) {
			return "", errors.New("invalid bodyCursor")
		}
		if input.IncludeComments != nil && *input.IncludeComments {
			return "", errors.New("body continuation requires includeComments=false")
		}
		// A continuation must not fetch the comment preview again.
		include = false
	}
	if !include && input.CommentLimit != nil {
		return "", errors.New("commentLimit requires includeComments=true")
	}
	q := url.Values{"includeComments": {strconv.FormatBool(include)}}
	if input.BodyLimit != nil {
		q.Set("bodyLimit", strconv.Itoa(*input.BodyLimit))
	}
	if resuming {
		q.Set("bodyCursor", input.BodyCursor)
	}
	if include && input.CommentLimit != nil {
		q.Set("commentLimit", strconv.Itoa(*input.CommentLimit))
	}
	return "/api/v1/community/posts/" + kind + "/" + input.ID + "?" + q.Encode(), nil
}

func commentPath(input CommentInput) (string, error) {
	if !validID(input.ID) {
		return "", errors.New("invalid comment target")
	}
	switch strings.TrimSpace(input.Kind) {
	case "dynamic", "article", "comment":
	default:
		return "", errors.New("invalid comment target kind")
	}
	if err := checkLimit(input.Limit, maxListLimit); err != nil {
		return "", err
	}
	if !validCursor(input.Cursor) {
		return "", errors.New("invalid cursor")
	}
	q := url.Values{"kind": {strings.TrimSpace(input.Kind)}, "id": {input.ID}}
	if input.Sort != "" {
		if (input.Sort != "new" && input.Sort != "hot") || (input.Sort == "hot" && strings.TrimSpace(input.Kind) == "comment") {
			return "", errors.New("sort must be new, or hot for post comments")
		}
		q.Set("sort", input.Sort)
	}
	if input.Limit != nil {
		q.Set("limit", strconv.Itoa(*input.Limit))
	}
	if input.Cursor != "" {
		q.Set("cursor", input.Cursor)
	}
	return "/api/v1/community/comments?" + q.Encode(), nil
}

func validAuthorID(raw string) bool {
	return raw == "me" || transport.ValidUserID(raw)
}

var notificationTypes = []string{"topic_like", "article_like", "comment_like",
	"topic_comment", "article_comment", "comment_reply", "follow"}

func statsPath(input StatsInput) (string, error) {
	subject := strings.TrimSpace(input.Subject)
	if subject == "" {
		subject = "owner"
	}
	aiIdentityID := strings.TrimSpace(input.AIIdentityID)
	switch {
	case subject == "owner" && aiIdentityID == "":
	case subject == "ai" && validOpaqueID(aiIdentityID):
	default:
		return "", errors.New("subject must be owner without aiIdentityId, or ai with one")
	}
	q := url.Values{"subject": {subject}}
	if aiIdentityID != "" {
		q.Set("aiIdentityId", aiIdentityID)
	}
	return "/api/v1/community/me/stats?" + q.Encode(), nil
}

func notificationsPath(input NotificationsInput) (string, error) {
	status := strings.ToLower(strings.TrimSpace(input.Status))
	if status != "" && status != "unread" && status != "read" {
		return "", errors.New("status must be unread, read, or omitted")
	}
	notifyType := strings.TrimSpace(input.Type)
	if notifyType != "" && !slices.Contains(notificationTypes, notifyType) {
		return "", errors.New("unsupported notification type")
	}
	if err := checkLimit(input.Limit, maxListLimit); err != nil {
		return "", err
	}
	if !validCursor(input.Cursor) {
		return "", errors.New("invalid cursor")
	}
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if notifyType != "" {
		q.Set("type", notifyType)
	}
	if input.Limit != nil {
		q.Set("limit", strconv.Itoa(*input.Limit))
	}
	if input.Cursor != "" {
		q.Set("cursor", input.Cursor)
	}
	return "/api/v1/community/notifications?" + q.Encode(), nil
}

// validOpaqueID admits bounded printable IDs (AI identity IDs are opaque
// strings) while rejecting control characters and separator injection.
func validOpaqueID(raw string) bool {
	if len(raw) < 1 || len(raw) > 64 {
		return false
	}
	for _, ch := range raw {
		if ch < 33 || ch > 126 {
			return false
		}
	}
	return true
}
