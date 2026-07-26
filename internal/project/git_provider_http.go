package project

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxProviderResponse = 2 << 20

type ProviderHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// ProviderRequestAuthorizer resolves a write-only credential reference and
// least-authority grant directly onto a request. Secret values never enter a
// project request, durable state, log, diff, or model-visible result.
type ProviderRequestAuthorizer interface {
	AuthorizeProviderRequest(context.Context, string, ProviderContext, *http.Request) error
}

type GitHubProvider struct {
	baseURL    *url.URL
	client     ProviderHTTPClient
	authorizer ProviderRequestAuthorizer
}

type GitLabProvider struct {
	baseURL    *url.URL
	client     ProviderHTTPClient
	authorizer ProviderRequestAuthorizer
}

func NewGitHubProvider(baseURL string, client ProviderHTTPClient,
	authorizer ProviderRequestAuthorizer) (*GitHubProvider, error) {
	parsed, err := providerBaseURL(baseURL, "https://api.github.com")
	if err != nil || client == nil {
		return nil, fmt.Errorf("project: valid GitHub provider transport is required")
	}
	return &GitHubProvider{baseURL: parsed, client: client, authorizer: authorizer}, nil
}

func NewGitLabProvider(baseURL string, client ProviderHTTPClient,
	authorizer ProviderRequestAuthorizer) (*GitLabProvider, error) {
	parsed, err := providerBaseURL(baseURL, "https://gitlab.com/api/v4")
	if err != nil || client == nil {
		return nil, fmt.Errorf("project: valid GitLab provider transport is required")
	}
	return &GitLabProvider{baseURL: parsed, client: client, authorizer: authorizer}, nil
}

func providerBaseURL(raw, fallback string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		raw = fallback
	}
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("project: provider base URL must be credential-free HTTPS")
	}
	return parsed, nil
}

func (provider *GitHubProvider) Name() string { return "github" }
func (provider *GitLabProvider) Name() string { return "gitlab" }

func (provider *GitHubProvider) ListRepositories(ctx context.Context, auth ProviderContext,
	cursor string) (ProviderPage[ProviderRepository], error) {
	var raw []struct {
		ID            int64  `json:"id"`
		Name          string `json:"full_name"`
		HTMLURL       string `json:"html_url"`
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
	}
	next, err := provider.getPage(ctx, auth, "/user/repos", nil, cursor, &raw)
	result := ProviderPage[ProviderRepository]{NextCursor: next}
	for _, item := range raw {
		result.Items = append(result.Items, ProviderRepository{ID: strconv.FormatInt(item.ID, 10), Name: item.Name,
			WebURL: item.HTMLURL, DefaultBranch: item.DefaultBranch, Private: item.Private})
	}
	return result, err
}

func (provider *GitHubProvider) ListIssues(ctx context.Context, auth ProviderContext, repository,
	cursor string) (ProviderPage[ProviderIssue], error) {
	var raw []struct {
		ID        int64     `json:"id"`
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		State     string    `json:"state"`
		HTMLURL   string    `json:"html_url"`
		UpdatedAt time.Time `json:"updated_at"`
		Pull      any       `json:"pull_request"`
	}
	next, err := provider.getPage(ctx, auth, "/repos/"+githubRepositoryPath(repository)+"/issues",
		url.Values{"state": {"all"}}, cursor, &raw)
	result := ProviderPage[ProviderIssue]{NextCursor: next}
	for _, item := range raw {
		if item.Pull != nil {
			continue
		}
		result.Items = append(result.Items, ProviderIssue{ID: strconv.FormatInt(item.ID, 10), Number: item.Number,
			Title: item.Title, State: item.State, WebURL: item.HTMLURL, UpdatedAt: item.UpdatedAt})
	}
	return result, err
}

func (provider *GitHubProvider) ListChanges(ctx context.Context, auth ProviderContext, repository,
	cursor string) (ProviderPage[ProviderChange], error) {
	var raw []githubPull
	next, err := provider.getPage(ctx, auth, "/repos/"+githubRepositoryPath(repository)+"/pulls",
		url.Values{"state": {"all"}}, cursor, &raw)
	result := ProviderPage[ProviderChange]{NextCursor: next}
	for _, item := range raw {
		result.Items = append(result.Items, item.normalized())
	}
	return result, err
}

func (provider *GitHubProvider) ListReviewThreads(ctx context.Context, auth ProviderContext, repository,
	change, cursor string) (ProviderPage[ProviderReviewThread], error) {
	if _, err := strconv.Atoi(change); err != nil {
		return ProviderPage[ProviderReviewThread]{}, fmt.Errorf("project: numeric GitHub change is required")
	}
	var raw []struct {
		ID       int64  `json:"id"`
		Path     string `json:"path"`
		Line     int    `json:"line"`
		Body     string `json:"body"`
		Position *int   `json:"position"`
	}
	next, err := provider.getPage(ctx, auth, "/repos/"+githubRepositoryPath(repository)+"/pulls/"+change+"/comments", nil, cursor, &raw)
	result := ProviderPage[ProviderReviewThread]{NextCursor: next}
	for _, item := range raw {
		result.Items = append(result.Items, ProviderReviewThread{ID: strconv.FormatInt(item.ID, 10), Path: item.Path,
			Line: item.Line, Body: item.Body, Outdated: item.Position == nil})
	}
	return result, err
}

func (provider *GitHubProvider) ListChecks(ctx context.Context, auth ProviderContext, repository,
	revision, cursor string) (ProviderPage[ProviderCheck], error) {
	var raw struct {
		Checks []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HTMLURL    string `json:"html_url"`
		} `json:"check_runs"`
	}
	next, err := provider.getPage(ctx, auth, "/repos/"+githubRepositoryPath(repository)+"/commits/"+url.PathEscape(revision)+"/check-runs", nil, cursor, &raw)
	result := ProviderPage[ProviderCheck]{NextCursor: next}
	for _, item := range raw.Checks {
		result.Items = append(result.Items, ProviderCheck{ID: strconv.FormatInt(item.ID, 10), Name: item.Name,
			Status: item.Status, Conclusion: item.Conclusion, WebURL: item.HTMLURL})
	}
	return result, err
}

func (provider *GitHubProvider) Mergeability(ctx context.Context, auth ProviderContext,
	repository string, number int) (ProviderMergeability, error) {
	var raw struct {
		Mergeable *bool  `json:"mergeable"`
		State     string `json:"mergeable_state"`
	}
	err := provider.request(ctx, auth, http.MethodGet,
		"/repos/"+githubRepositoryPath(repository)+"/pulls/"+strconv.Itoa(number), nil, nil, &raw)
	result := ProviderMergeability{State: raw.State}
	if raw.Mergeable != nil {
		result.Mergeable = *raw.Mergeable
	} else {
		result.Reasons = []string{"provider is still computing mergeability"}
	}
	return result, err
}

func (provider *GitHubProvider) FindDraftByMarker(ctx context.Context, auth ProviderContext,
	repository, marker string) (*ProviderChange, error) {
	cursor := ""
	for page := 0; page < 10; page++ {
		var raw []githubPull
		next, err := provider.getPage(ctx, auth, "/repos/"+githubRepositoryPath(repository)+"/pulls",
			url.Values{"state": {"all"}}, cursor, &raw)
		if err != nil {
			return nil, err
		}
		for _, item := range raw {
			if strings.Contains(item.Body, marker) {
				result := item.normalized()
				return &result, nil
			}
		}
		if next == "" {
			return nil, nil
		}
		cursor = next
	}
	return nil, fmt.Errorf("project: provider marker search exceeded pagination bound")
}

func (provider *GitHubProvider) CreateDraftChange(ctx context.Context, auth ProviderContext,
	input ProviderDraftInput) (ProviderChange, error) {
	payload := map[string]any{"head": input.SourceBranch, "base": input.TargetBranch,
		"title": input.Title, "body": input.Body, "draft": true}
	var raw githubPull
	err := provider.request(ctx, auth, http.MethodPost, "/repos/"+githubRepositoryPath(input.Repository)+"/pulls", nil, payload, &raw)
	return raw.normalized(), err
}

type githubPull struct {
	ID        int64     `json:"id"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	Draft     bool      `json:"draft"`
	HTMLURL   string    `json:"html_url"`
	UpdatedAt time.Time `json:"updated_at"`
	Head      struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (item githubPull) normalized() ProviderChange {
	return ProviderChange{ID: strconv.FormatInt(item.ID, 10), Number: item.Number, Title: item.Title,
		State: item.State, Draft: item.Draft, SourceBranch: item.Head.Ref, TargetBranch: item.Base.Ref,
		WebURL: item.HTMLURL, UpdatedAt: item.UpdatedAt}
}

func (provider *GitHubProvider) getPage(ctx context.Context, auth ProviderContext, path string,
	values url.Values, cursor string, result any) (string, error) {
	page, err := providerPageNumber(cursor)
	if err != nil {
		return "", err
	}
	if values == nil {
		values = url.Values{}
	}
	values.Set("per_page", "100")
	values.Set("page", strconv.Itoa(page))
	if err := provider.request(ctx, auth, http.MethodGet, path, values, nil, result); err != nil {
		return "", err
	}
	length := reflectedSliceLength(result)
	if length == 100 {
		return strconv.Itoa(page + 1), nil
	}
	return "", nil
}

func (provider *GitHubProvider) request(ctx context.Context, auth ProviderContext, method, path string,
	values url.Values, payload any, result any) error {
	return providerRequest(ctx, "github", provider.baseURL, provider.client, provider.authorizer,
		auth, method, path, values, payload, result, map[string]string{"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"})
}

// ListRepositories returns repositories using the normalized GitLab cursor contract.
func (provider *GitLabProvider) ListRepositories(ctx context.Context, auth ProviderContext, cursor string) (ProviderPage[ProviderRepository], error) {
	var raw []struct {
		ID            int64  `json:"id"`
		Path          string `json:"path_with_namespace"`
		WebURL        string `json:"web_url"`
		DefaultBranch string `json:"default_branch"`
		Visibility    string `json:"visibility"`
	}
	next, err := provider.getPage(ctx, auth, "/projects", url.Values{"membership": {"true"}}, cursor, &raw)
	result := ProviderPage[ProviderRepository]{NextCursor: next}
	for _, item := range raw {
		result.Items = append(result.Items, ProviderRepository{ID: strconv.FormatInt(item.ID, 10), Name: item.Path, WebURL: item.WebURL, DefaultBranch: item.DefaultBranch, Private: item.Visibility == "private"})
	}
	return result, err
}

func (provider *GitLabProvider) ListIssues(ctx context.Context, auth ProviderContext, repository, cursor string) (ProviderPage[ProviderIssue], error) {
	var raw []struct {
		ID        int64     `json:"id"`
		IID       int       `json:"iid"`
		Title     string    `json:"title"`
		State     string    `json:"state"`
		WebURL    string    `json:"web_url"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	next, err := provider.getPage(ctx, auth, gitlabProjectPath(repository)+"/issues", url.Values{"scope": {"all"}}, cursor, &raw)
	result := ProviderPage[ProviderIssue]{NextCursor: next}
	for _, item := range raw {
		result.Items = append(result.Items, ProviderIssue{ID: strconv.FormatInt(item.ID, 10), Number: item.IID, Title: item.Title, State: item.State, WebURL: item.WebURL, UpdatedAt: item.UpdatedAt})
	}
	return result, err
}

type gitlabMergeRequest struct {
	ID          int64     `json:"id"`
	IID         int       `json:"iid"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	State       string    `json:"state"`
	Draft       bool      `json:"draft"`
	Source      string    `json:"source_branch"`
	Target      string    `json:"target_branch"`
	WebURL      string    `json:"web_url"`
	UpdatedAt   time.Time `json:"updated_at"`
	MergeStatus string    `json:"detailed_merge_status"`
}

func (item gitlabMergeRequest) normalized() ProviderChange {
	return ProviderChange{ID: strconv.FormatInt(item.ID, 10), Number: item.IID, Title: item.Title, State: item.State, Draft: item.Draft, SourceBranch: item.Source, TargetBranch: item.Target, WebURL: item.WebURL, UpdatedAt: item.UpdatedAt}
}

func (provider *GitLabProvider) ListChanges(ctx context.Context, auth ProviderContext, repository, cursor string) (ProviderPage[ProviderChange], error) {
	var raw []gitlabMergeRequest
	next, err := provider.getPage(ctx, auth, gitlabProjectPath(repository)+"/merge_requests", url.Values{"scope": {"all"}}, cursor, &raw)
	result := ProviderPage[ProviderChange]{NextCursor: next}
	for _, item := range raw {
		result.Items = append(result.Items, item.normalized())
	}
	return result, err
}

func (provider *GitLabProvider) ListReviewThreads(ctx context.Context, auth ProviderContext, repository, change, cursor string) (ProviderPage[ProviderReviewThread], error) {
	var raw []struct {
		ID       string `json:"id"`
		Resolved bool   `json:"resolved"`
		Notes    []struct {
			ID       int64  `json:"id"`
			Body     string `json:"body"`
			Resolved bool   `json:"resolved"`
			Position *struct {
				NewPath string `json:"new_path"`
				NewLine int    `json:"new_line"`
			} `json:"position"`
		} `json:"notes"`
	}
	next, err := provider.getPage(ctx, auth, gitlabProjectPath(repository)+"/merge_requests/"+url.PathEscape(change)+"/discussions", nil, cursor, &raw)
	result := ProviderPage[ProviderReviewThread]{NextCursor: next}
	for _, discussion := range raw {
		for _, note := range discussion.Notes {
			item := ProviderReviewThread{ID: discussion.ID + ":" + strconv.FormatInt(note.ID, 10), Body: note.Body, Resolved: discussion.Resolved || note.Resolved}
			if note.Position != nil {
				item.Path = note.Position.NewPath
				item.Line = note.Position.NewLine
			} else {
				item.Outdated = true
			}
			result.Items = append(result.Items, item)
		}
	}
	return result, err
}

func (provider *GitLabProvider) ListChecks(ctx context.Context, auth ProviderContext, repository, revision, cursor string) (ProviderPage[ProviderCheck], error) {
	var raw []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
		WebURL string `json:"web_url"`
	}
	next, err := provider.getPage(ctx, auth, gitlabProjectPath(repository)+"/repository/commits/"+url.PathEscape(revision)+"/statuses", nil, cursor, &raw)
	result := ProviderPage[ProviderCheck]{NextCursor: next}
	for _, item := range raw {
		result.Items = append(result.Items, ProviderCheck{ID: strconv.FormatInt(item.ID, 10), Name: item.Name, Status: item.Status, Conclusion: item.Status, WebURL: item.WebURL})
	}
	return result, err
}

func (provider *GitLabProvider) Mergeability(ctx context.Context, auth ProviderContext, repository string, number int) (ProviderMergeability, error) {
	var raw gitlabMergeRequest
	err := provider.request(ctx, auth, http.MethodGet, gitlabProjectPath(repository)+"/merge_requests/"+strconv.Itoa(number), nil, nil, &raw)
	mergeable := raw.MergeStatus == "mergeable" || raw.MergeStatus == "can_be_merged"
	result := ProviderMergeability{Mergeable: mergeable, State: raw.MergeStatus}
	if !mergeable && raw.MergeStatus != "" {
		result.Reasons = []string{raw.MergeStatus}
	}
	return result, err
}

func (provider *GitLabProvider) FindDraftByMarker(ctx context.Context, auth ProviderContext, repository, marker string) (*ProviderChange, error) {
	cursor := ""
	for page := 0; page < 10; page++ {
		var raw []gitlabMergeRequest
		next, err := provider.getPage(ctx, auth, gitlabProjectPath(repository)+"/merge_requests", url.Values{"scope": {"all"}}, cursor, &raw)
		if err != nil {
			return nil, err
		}
		for _, item := range raw {
			if strings.Contains(item.Description, marker) {
				normalized := item.normalized()
				return &normalized, nil
			}
		}
		if next == "" {
			return nil, nil
		}
		cursor = next
	}
	return nil, fmt.Errorf("project: provider marker search exceeded pagination bound")
}

func (provider *GitLabProvider) CreateDraftChange(ctx context.Context, auth ProviderContext, input ProviderDraftInput) (ProviderChange, error) {
	payload := map[string]any{"source_branch": input.SourceBranch, "target_branch": input.TargetBranch, "title": "Draft: " + input.Title, "description": input.Body}
	var raw gitlabMergeRequest
	err := provider.request(ctx, auth, http.MethodPost, gitlabProjectPath(input.Repository)+"/merge_requests", nil, payload, &raw)
	return raw.normalized(), err
}

func (provider *GitLabProvider) getPage(ctx context.Context, auth ProviderContext, path string, values url.Values, cursor string, result any) (string, error) {
	page, err := providerPageNumber(cursor)
	if err != nil {
		return "", err
	}
	if values == nil {
		values = url.Values{}
	}
	values.Set("per_page", "100")
	values.Set("page", strconv.Itoa(page))
	if err := provider.request(ctx, auth, http.MethodGet, path, values, nil, result); err != nil {
		return "", err
	}
	if reflectedSliceLength(result) == 100 {
		return strconv.Itoa(page + 1), nil
	}
	return "", nil
}
func (provider *GitLabProvider) request(ctx context.Context, auth ProviderContext, method, path string, values url.Values, payload, result any) error {
	return providerRequest(ctx, "gitlab", provider.baseURL, provider.client, provider.authorizer, auth, method, path, values, payload, result, nil)
}

func providerRequest(ctx context.Context, provider string, base *url.URL, client ProviderHTTPClient, authorizer ProviderRequestAuthorizer, auth ProviderContext, method, path string, values url.Values, payload, result any, headers map[string]string) error {
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(path, "/")
	target.RawQuery = values.Encode()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Ion-Software-Studio/1")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if strings.TrimSpace(auth.CredentialReference) != "" {
		if authorizer == nil {
			return fmt.Errorf("project: provider credential broker is unavailable")
		}
		if err := authorizer.AuthorizeProviderRequest(ctx, provider, auth, request); err != nil {
			return err
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxProviderResponse+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(raw) > maxProviderResponse {
		return fmt.Errorf("project: provider response exceeds bound")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("project: %s provider returned HTTP %d", provider, response.StatusCode)
	}
	if result == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("project: invalid %s provider response", provider)
	}
	return nil
}

func providerPageNumber(cursor string) (int, error) {
	if cursor == "" {
		return 1, nil
	}
	page, err := strconv.Atoi(cursor)
	if err != nil || page < 1 || page > 20 {
		return 0, fmt.Errorf("project: invalid provider cursor")
	}
	return page, nil
}
func reflectedSliceLength(value any) int {
	raw, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) == nil {
		return len(items)
	}
	var wrapper struct {
		Checks []json.RawMessage `json:"check_runs"`
	}
	if json.Unmarshal(raw, &wrapper) == nil {
		return len(wrapper.Checks)
	}
	return 0
}
func githubRepositoryPath(repository string) string {
	parts := strings.Split(strings.Trim(repository, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "invalid/invalid"
	}
	return url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
}
func gitlabProjectPath(repository string) string {
	return "/projects/" + url.PathEscape(strings.Trim(repository, "/"))
}
