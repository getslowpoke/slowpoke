package tasks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type seen struct {
	method, path, query, auth, privateToken string
	body                                    map[string]any
}

func fake(t *testing.T, response string) (*httptest.Server, *seen) {
	s := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &s.body)
		s.method, s.path, s.query, s.auth, s.privateToken = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("PRIVATE-TOKEN")
		w.WriteHeader(201)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

var issue = Issue{Title: "Slow query: select * from orders", Body: "## Diagnosis\nAdd an index", Assignee: "ada", Labels: []string{"performance"}}

func TestGitHub(t *testing.T) {
	srv, s := fake(t, `{"number":42,"html_url":"https://github.com/acme/shop/issues/42"}`)
	res, err := Create(context.Background(), Integration{Provider: "github", Token: "ghp_x", Repo: "acme/shop", BaseURL: srv.URL}, issue)
	if err != nil || res.ExternalID != "42" || res.URL != "https://github.com/acme/shop/issues/42" {
		t.Fatalf("%+v %v", res, err)
	}
	if s.path != "/repos/acme/shop/issues" || s.auth != "Bearer ghp_x" || s.body["title"] != issue.Title || s.body["assignees"].([]any)[0] != "ada" {
		t.Fatalf("%+v", s)
	}
}

func TestGitLab(t *testing.T) {
	srv, s := fake(t, `{"iid":7,"web_url":"https://gitlab.com/acme/shop/-/issues/7"}`)
	res, err := Create(context.Background(), Integration{Provider: "gitlab", Token: "glpat", Repo: "acme/shop", BaseURL: srv.URL}, issue)
	if err != nil || res.ExternalID != "7" {
		t.Fatalf("%+v %v", res, err)
	}
	if s.path != "/api/v4/projects/acme/shop/issues" || s.privateToken != "glpat" || s.body["description"] != issue.Body || s.body["labels"] != "performance" {
		t.Fatalf("%+v", s)
	}
}

func TestLinear(t *testing.T) {
	srv, s := fake(t, `{"data":{"issueCreate":{"success":true,"issue":{"identifier":"ENG-12","url":"https://linear.app/acme/issue/ENG-12"}}}}`)
	res, err := Create(context.Background(), Integration{Provider: "linear", Token: "lin_api", TeamID: "team-1", BaseURL: srv.URL}, issue)
	if err != nil || res.ExternalID != "ENG-12" {
		t.Fatalf("%+v %v", res, err)
	}
	vars := s.body["variables"].(map[string]any)["input"].(map[string]any)
	if s.auth != "lin_api" || vars["teamId"] != "team-1" || vars["assigneeId"] != "ada" || !strings.Contains(s.body["query"].(string), "issueCreate") {
		t.Fatalf("%+v", s)
	}
}

func TestLinearErrorsAreReported(t *testing.T) {
	srv, _ := fake(t, `{"errors":[{"message":"team not found"}]}`)
	if _, err := Create(context.Background(), Integration{Provider: "linear", Token: "x", TeamID: "t", BaseURL: srv.URL}, issue); err == nil || !strings.Contains(err.Error(), "team not found") {
		t.Fatalf("err %v", err)
	}
}

func TestJira(t *testing.T) {
	srv, s := fake(t, `{"id":"10001","key":"SHOP-9"}`)
	res, err := Create(context.Background(), Integration{Provider: "jira", Token: "tok", Email: "ada@acme.io", ProjectKey: "SHOP", BaseURL: srv.URL}, issue)
	if err != nil || res.ExternalID != "SHOP-9" || res.URL != srv.URL+"/browse/SHOP-9" {
		t.Fatalf("%+v %v", res, err)
	}
	fields := s.body["fields"].(map[string]any)
	if s.path != "/rest/api/3/issue" || !strings.HasPrefix(s.auth, "Basic ") || fields["summary"] != issue.Title || fields["issuetype"].(map[string]any)["name"] != "Task" {
		t.Fatalf("%+v", s)
	}
	if fields["description"].(map[string]any)["type"] != "doc" {
		t.Fatal("jira v3 wants an Atlassian document")
	}
}

func TestAsanaTrelloClickUp(t *testing.T) {
	srv, s := fake(t, `{"data":{"gid":"123","permalink_url":"https://app.asana.com/0/1/123"}}`)
	res, err := Create(context.Background(), Integration{Provider: "asana", Token: "a", ProjectID: "p1", BaseURL: srv.URL}, issue)
	if err != nil || res.ExternalID != "123" || s.path != "/tasks" || s.body["data"].(map[string]any)["projects"].([]any)[0] != "p1" {
		t.Fatalf("asana %+v %v %+v", res, err, s)
	}

	srv, s = fake(t, `{"id":"card1","shortUrl":"https://trello.com/c/abc"}`)
	res, err = Create(context.Background(), Integration{Provider: "trello", Token: "key123:tok456", ListID: "list9", BaseURL: srv.URL}, issue)
	if err != nil || res.URL != "https://trello.com/c/abc" || s.path != "/cards" || !strings.Contains(s.query, "key=key123") || !strings.Contains(s.query, "token=tok456") || s.body["idList"] != "list9" {
		t.Fatalf("trello %+v %v %+v", res, err, s)
	}

	srv, s = fake(t, `{"id":"cu1","url":"https://app.clickup.com/t/cu1"}`)
	res, err = Create(context.Background(), Integration{Provider: "clickup", Token: "pk_1", ListID: "l2", BaseURL: srv.URL}, Issue{Title: "x", Body: "y", Assignee: "183"})
	if err != nil || res.ExternalID != "cu1" || s.path != "/list/l2/task" || s.auth != "pk_1" || s.body["assignees"].([]any)[0].(float64) != 183 {
		t.Fatalf("clickup %+v %v %+v", res, err, s)
	}
}

func TestHTTPErrorsAndValidation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"message":"Bad credentials"}`)
	}))
	defer srv.Close()
	if _, err := Create(context.Background(), Integration{Provider: "github", Token: "x", Repo: "a/b", BaseURL: srv.URL}, issue); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err %v", err)
	}
	for _, bad := range []Integration{
		{ID: "gh", Provider: "github", Token: "x", Repo: "nope"},
		{ID: "j", Provider: "jira", Token: "x", ProjectKey: "S"},
		{ID: "t", Provider: "trello", Token: "only-token", ListID: "l"},
		{ID: "x", Provider: "notion", Token: "x"},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v must be invalid", bad)
		}
	}
}
