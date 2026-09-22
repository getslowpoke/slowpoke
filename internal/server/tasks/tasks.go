// Package tasks creates issues and cards for missions in the team's tracker:
// GitHub, GitLab, Linear, Jira, Asana, Trello and ClickUp.
package tasks

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var Providers = []string{"github", "gitlab", "linear", "jira", "asana", "trello", "clickup"}

// Integration is configured per project (settings.integrations.tasks).
type Integration struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Provider   string   `json:"provider"`
	Token      string   `json:"token,omitempty"`
	BaseURL    string   `json:"base_url,omitempty"`    // GitLab/Jira self-hosted; overrides the API host
	Repo       string   `json:"repo,omitempty"`        // GitHub/GitLab: owner/name
	TeamID     string   `json:"team_id,omitempty"`     // Linear
	Email      string   `json:"email,omitempty"`       // Jira (basic auth)
	ProjectKey string   `json:"project_key,omitempty"` // Jira
	IssueType  string   `json:"issue_type,omitempty"`  // Jira, default Task
	ProjectID  string   `json:"project_id,omitempty"`  // Asana project gid
	ListID     string   `json:"list_id,omitempty"`     // Trello/ClickUp
	Labels     []string `json:"labels,omitempty"`
}

type Issue struct {
	Title    string
	Body     string // Markdown
	Assignee string // provider user: GitHub login, Linear/Jira/Asana/Trello id, ClickUp numeric id
	Labels   []string
}

type Result struct {
	ExternalID string
	URL        string
}

func (i Integration) Validate() error {
	var missing []string
	need := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, field)
		}
	}
	need("id", i.ID)
	need("token", i.Token)
	switch i.Provider {
	case "github", "gitlab":
		if !strings.Contains(i.Repo, "/") {
			missing = append(missing, "repo (owner/name)")
		}
	case "linear":
		need("team_id", i.TeamID)
	case "jira":
		need("base_url", i.BaseURL)
		need("email", i.Email)
		need("project_key", i.ProjectKey)
	case "asana":
		need("project_id", i.ProjectID)
	case "trello":
		need("list_id", i.ListID)
		if !strings.Contains(i.Token, ":") {
			missing = append(missing, "token as key:token")
		}
	case "clickup":
		need("list_id", i.ListID)
	default:
		return fmt.Errorf("task integration %q: unknown provider %q", i.ID, i.Provider)
	}
	if len(missing) > 0 {
		return fmt.Errorf("task integration %q (%s): missing %s", i.ID, i.Provider, strings.Join(missing, ", "))
	}
	return nil
}

var client = &http.Client{Timeout: 20 * time.Second}

func base(i Integration, fallback string) string {
	if i.BaseURL != "" {
		return strings.TrimRight(i.BaseURL, "/")
	}
	return fallback
}

func call(ctx context.Context, method, endpoint string, headers map[string]string, body any, out any) error {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "slowpoke")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, msg)
	}
	return json.Unmarshal(data, out)
}

func Create(ctx context.Context, i Integration, issue Issue) (Result, error) {
	labels := append(append([]string{}, i.Labels...), issue.Labels...)
	switch i.Provider {
	case "github":
		var out struct {
			Number  int    `json:"number"`
			HTMLURL string `json:"html_url"`
		}
		body := map[string]any{"title": issue.Title, "body": issue.Body, "labels": labels}
		if issue.Assignee != "" {
			body["assignees"] = []string{issue.Assignee}
		}
		err := call(ctx, "POST", base(i, "https://api.github.com")+"/repos/"+i.Repo+"/issues",
			map[string]string{"Authorization": "Bearer " + i.Token, "X-GitHub-Api-Version": "2022-11-28"}, body, &out)
		return Result{ExternalID: strconv.Itoa(out.Number), URL: out.HTMLURL}, err

	case "gitlab":
		var out struct {
			IID    int    `json:"iid"`
			WebURL string `json:"web_url"`
		}
		body := map[string]any{"title": issue.Title, "description": issue.Body, "labels": strings.Join(labels, ",")}
		if id, err := strconv.Atoi(issue.Assignee); err == nil {
			body["assignee_ids"] = []int{id}
		}
		err := call(ctx, "POST", base(i, "https://gitlab.com")+"/api/v4/projects/"+url.PathEscape(i.Repo)+"/issues",
			map[string]string{"PRIVATE-TOKEN": i.Token}, body, &out)
		return Result{ExternalID: strconv.Itoa(out.IID), URL: out.WebURL}, err

	case "linear":
		var out struct {
			Data struct {
				IssueCreate struct {
					Success bool `json:"success"`
					Issue   struct {
						Identifier string `json:"identifier"`
						URL        string `json:"url"`
					} `json:"issue"`
				} `json:"issueCreate"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		input := map[string]any{"teamId": i.TeamID, "title": issue.Title, "description": issue.Body}
		if issue.Assignee != "" {
			input["assigneeId"] = issue.Assignee
		}
		body := map[string]any{
			"query":     "mutation($input: IssueCreateInput!) { issueCreate(input: $input) { success issue { identifier url } } }",
			"variables": map[string]any{"input": input},
		}
		if err := call(ctx, "POST", base(i, "https://api.linear.app/graphql"), map[string]string{"Authorization": i.Token}, body, &out); err != nil {
			return Result{}, err
		}
		if len(out.Errors) > 0 {
			return Result{}, errors.New("linear: " + out.Errors[0].Message)
		}
		return Result{ExternalID: out.Data.IssueCreate.Issue.Identifier, URL: out.Data.IssueCreate.Issue.URL}, nil

	case "jira":
		var out struct {
			Key string `json:"key"`
		}
		issueType := i.IssueType
		if issueType == "" {
			issueType = "Task"
		}
		fields := map[string]any{
			"project":     map[string]string{"key": i.ProjectKey},
			"summary":     issue.Title,
			"issuetype":   map[string]string{"name": issueType},
			"description": atlassianDoc(issue.Body),
		}
		if len(labels) > 0 {
			fields["labels"] = labels
		}
		if issue.Assignee != "" {
			fields["assignee"] = map[string]string{"accountId": issue.Assignee}
		}
		auth := base64.StdEncoding.EncodeToString([]byte(i.Email + ":" + i.Token))
		err := call(ctx, "POST", base(i, "")+"/rest/api/3/issue", map[string]string{"Authorization": "Basic " + auth}, map[string]any{"fields": fields}, &out)
		return Result{ExternalID: out.Key, URL: base(i, "") + "/browse/" + out.Key}, err

	case "asana":
		var out struct {
			Data struct {
				GID          string `json:"gid"`
				PermalinkURL string `json:"permalink_url"`
			} `json:"data"`
		}
		data := map[string]any{"name": issue.Title, "notes": issue.Body, "projects": []string{i.ProjectID}}
		if issue.Assignee != "" {
			data["assignee"] = issue.Assignee
		}
		err := call(ctx, "POST", base(i, "https://app.asana.com/api/1.0")+"/tasks", map[string]string{"Authorization": "Bearer " + i.Token}, map[string]any{"data": data}, &out)
		return Result{ExternalID: out.Data.GID, URL: out.Data.PermalinkURL}, err

	case "trello":
		var out struct {
			ID       string `json:"id"`
			ShortURL string `json:"shortUrl"`
		}
		key, token, _ := strings.Cut(i.Token, ":")
		body := map[string]any{"idList": i.ListID, "name": issue.Title, "desc": issue.Body}
		if issue.Assignee != "" {
			body["idMembers"] = []string{issue.Assignee}
		}
		q := url.Values{"key": {key}, "token": {token}}
		err := call(ctx, "POST", base(i, "https://api.trello.com/1")+"/cards?"+q.Encode(), nil, body, &out)
		return Result{ExternalID: out.ID, URL: out.ShortURL}, err

	case "clickup":
		var out struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		}
		body := map[string]any{"name": issue.Title, "markdown_description": issue.Body, "tags": labels}
		if id, err := strconv.Atoi(issue.Assignee); err == nil {
			body["assignees"] = []int{id}
		}
		err := call(ctx, "POST", base(i, "https://api.clickup.com/api/v2")+"/list/"+i.ListID+"/task", map[string]string{"Authorization": i.Token}, body, &out)
		return Result{ExternalID: out.ID, URL: out.URL}, err
	}
	return Result{}, fmt.Errorf("unknown provider %q", i.Provider)
}

// atlassianDoc wraps plain Markdown in the minimal Atlassian Document Format: one paragraph per block.
func atlassianDoc(markdown string) map[string]any {
	var content []any
	for _, block := range strings.Split(markdown, "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		content = append(content, map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": block}}})
	}
	return map[string]any{"type": "doc", "version": 1, "content": content}
}
