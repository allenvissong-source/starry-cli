package community

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

func TestReadFiltersValidateAndStayBoundToContinuation(t *testing.T) {
	week := 2
	type readCase struct {
		name string
		path string
		err  error
		want url.Values
	}
	path, err := listPath(ListInput{Kind: "all", Source: "latest", NodeID: "1", TopicID: "2", TagID: "3", AuthorID: "1000", Sort: "hot", Cursor: "next"})
	cases := []readCase{{"list", path, err, url.Values{"kind": {"all"}, "source": {"latest"}, "nodeId": {"1"}, "topicId": {"2"}, "tagId": {"3"}, "authorId": {"1000"}, "sort": {"hot"}, "cursor": {"next"}}}}
	path, err = searchPath(SearchInput{Keyword: " 天气 ", NodeID: "2", TimeRange: &week, Cursor: "search"})
	cases = append(cases, readCase{"search", path, err, url.Values{"kind": {"all"}, "keyword": {"天气"}, "nodeId": {"2"}, "timeRange": {"2"}, "cursor": {"search"}}})
	for _, item := range cases {
		uri, parseErr := url.Parse(item.path)
		if item.err != nil || parseErr != nil || !reflect.DeepEqual(uri.Query(), item.want) {
			t.Fatalf("%s filters: %s %v", item.name, item.path, item.err)
		}
	}
	for _, input := range []ListInput{
		{Source: "recommended", Sort: "hot"}, {Source: "latest", Sort: "other"},
		{TagID: "0"}, {AuthorID: "owner:1"}, {AuthorID: "../owner?x=1"},
	} {
		if _, err := listPath(input); err == nil {
			t.Fatalf("invalid list accepted: %+v", input)
		}
	}
	badRange := 5
	if _, err := searchPath(SearchInput{Keyword: "天气", TimeRange: &badRange}); err == nil {
		t.Fatal("invalid search time range accepted")
	}
	if _, err := commentPath(CommentInput{Kind: "comment", ID: "1", Sort: "hot"}); err == nil {
		t.Fatal("hot reply ordering accepted")
	}
}

func TestMixedReadPagePreservesServerOrderAndCursorFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "old" {
			w.WriteHeader(409)
			w.Write([]byte(`{"success":false,"error":{"code":"cursor_expired","retryable":false}}`))
			return
		}
		w.Write([]byte(`{"success":true,"data":{"items":[{"kind":"article","id":"2"},{"kind":"dynamic","id":"9007199254740993"}],"page":{"hasMore":true,"nextCursor":"exact-next"}}}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{BackendURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.Call(context.Background(), "delegate", "community_list_posts", json.RawMessage(`{"kind":"all","source":"latest"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(page)
	want := `{"data":{"items":[{"id":"2","kind":"article"},{"id":"9007199254740993","kind":"dynamic"}],"page":{"hasMore":true,"nextCursor":"exact-next"}}}`
	var gotValue, wantValue any
	if err != nil || json.Unmarshal(raw, &gotValue) != nil || json.Unmarshal([]byte(want), &wantValue) != nil || !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("mixed page/order/precision changed: %s %v", raw, err)
	}
	if _, err := client.Call(context.Background(), "delegate", "community_list_posts", json.RawMessage(`{"cursor":"old"}`)); err == nil {
		t.Fatal("expired cursor became a fresh successful page")
	}
}
