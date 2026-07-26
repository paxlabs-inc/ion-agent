package project

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

type fakeRepositoryProvider struct {
	mu          sync.Mutex
	created     int
	stored      *ProviderChange
	loseCreate  bool
	loopCursors bool
}

func (*fakeRepositoryProvider) Name() string { return "fake" }
func (provider *fakeRepositoryProvider) ListRepositories(_ context.Context, _ ProviderContext, cursor string) (ProviderPage[ProviderRepository], error) {
	if cursor == "" {
		return ProviderPage[ProviderRepository]{Items: []ProviderRepository{{ID: "1", Name: "owner/repo"}}, NextCursor: "2"}, nil
	}
	next := ""
	if provider.loopCursors {
		next = "2"
	}
	return ProviderPage[ProviderRepository]{Items: []ProviderRepository{{ID: "2", Name: "owner/other"}}, NextCursor: next}, nil
}
func (*fakeRepositoryProvider) ListIssues(context.Context, ProviderContext, string, string) (ProviderPage[ProviderIssue], error) {
	return ProviderPage[ProviderIssue]{Items: []ProviderIssue{{ID: "i", Number: 1}}}, nil
}
func (*fakeRepositoryProvider) ListChanges(context.Context, ProviderContext, string, string) (ProviderPage[ProviderChange], error) {
	return ProviderPage[ProviderChange]{Items: []ProviderChange{{ID: "c", Number: 2}}}, nil
}
func (*fakeRepositoryProvider) ListReviewThreads(context.Context, ProviderContext, string, string, string) (ProviderPage[ProviderReviewThread], error) {
	return ProviderPage[ProviderReviewThread]{Items: []ProviderReviewThread{{ID: "r", Outdated: true}}}, nil
}
func (*fakeRepositoryProvider) ListChecks(context.Context, ProviderContext, string, string, string) (ProviderPage[ProviderCheck], error) {
	return ProviderPage[ProviderCheck]{Items: []ProviderCheck{{ID: "k", Status: "completed"}}}, nil
}
func (*fakeRepositoryProvider) Mergeability(context.Context, ProviderContext, string, int) (ProviderMergeability, error) {
	return ProviderMergeability{Mergeable: true, State: "clean"}, nil
}
func (provider *fakeRepositoryProvider) FindDraftByMarker(_ context.Context, _ ProviderContext, _, marker string) (*ProviderChange, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.stored != nil && strings.Contains(provider.stored.Title, marker) {
		result := *provider.stored
		return &result, nil
	}
	return nil, nil
}
func (provider *fakeRepositoryProvider) CreateDraftChange(_ context.Context, _ ProviderContext, input ProviderDraftInput) (ProviderChange, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.created++
	result := ProviderChange{ID: "draft", Number: 7, Title: input.Title + " " + input.Marker, Draft: true,
		SourceBranch: input.SourceBranch, TargetBranch: input.TargetBranch}
	provider.stored = &result
	if provider.loseCreate {
		provider.loseCreate = false
		return ProviderChange{}, errors.New("lost response")
	}
	return result, nil
}

func TestProviderGrantPaginationAndExactlyOnceDraftReconciliation(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	fake := &fakeRepositoryProvider{loseCreate: true}
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot,
		AttachRoots: []string{attachRoot}, RepositoryProviders: map[string]RepositoryProvider{"fake": fake}})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(attachRoot, "provider")
	writeIntelligenceFile(t, root, "README.md", "provider\n")
	runGit(t, "init", "--initial-branch=main", root)
	runGit(t, "-C", root, "add", ".")
	runGitWithIdentity(t, "-C", root, "commit", "-m", "baseline")
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "provider-attach"), AttachInput{Name: "Provider", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	readGrant, err := service.IssueRepositoryGrant(ctx, actor, RepositoryGrantRequest{ProjectID: project.ID,
		Provider: "fake", Repository: "*", Actions: []string{"read"}, TTLSeconds: 300}, false)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := service.ProviderQuery(ctx, actor, ProviderQueryRequest{ProjectID: project.ID,
		Provider: "fake", Repository: "owner/repo", PermissionGrant: readGrant}, "repositories")
	if err != nil || len(projection.Repositories) != 2 {
		t.Fatalf("provider pagination = %+v, %v", projection, err)
	}
	fake.loopCursors = true
	if _, err := service.ProviderQuery(ctx, actor, ProviderQueryRequest{ProjectID: project.ID,
		Provider: "fake", Repository: "owner/repo", PermissionGrant: readGrant}, "repositories"); err == nil {
		t.Fatal("provider cursor loop was accepted")
	}
	fake.loopCursors = false
	writeGrant, err := service.IssueRepositoryGrant(ctx, actor, RepositoryGrantRequest{ProjectID: project.ID,
		Provider: "fake", Repository: "owner/repo", Actions: []string{"draft.create"}, TTLSeconds: 300}, true)
	if err != nil {
		t.Fatal(err)
	}
	head, _ := exactGitHead(ctx, root)
	request := ProviderDraftRequest{ProjectID: project.ID, Provider: "fake", Repository: "owner/repo",
		SourceBranch: "feature", TargetBranch: "main", Title: "Draft", ExpectedHead: head,
		IdempotencyKey: "draft-exactly-once", PermissionGrant: writeGrant}
	if _, err := service.CreateProviderDraft(ctx, actor, request, true); err == nil {
		t.Fatal("simulated lost response unexpectedly succeeded")
	}
	reconciled, err := service.CreateProviderDraft(ctx, actor, request, true)
	if err != nil || reconciled.ID != "draft" || fake.created != 1 {
		t.Fatalf("draft reconciliation = %+v created=%d err=%v", reconciled, fake.created, err)
	}
	if _, err := service.CreateProviderDraft(ctx, uuid.New(), request, true); err == nil {
		t.Fatal("cross-actor repository grant was accepted")
	}
}

type recordingProviderAuthorizer struct {
	provider  string
	reference string
	grant     string
}

func (authorizer *recordingProviderAuthorizer) AuthorizeProviderRequest(_ context.Context, provider string,
	auth ProviderContext, request *http.Request) error {
	authorizer.provider, authorizer.reference, authorizer.grant = provider, auth.CredentialReference, auth.PermissionGrant
	request.Header.Set("Authorization", "Bearer resolved-outside-project")
	return nil
}

func TestConcreteGitHubAndGitLabAdaptersNormalizeWithoutLeakingCredentials(t *testing.T) {
	githubServer := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer resolved-outside-project" || !strings.Contains(request.URL.Path, "/comments") {
			t.Fatalf("GitHub request = %s auth=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`[{"id":9,"path":"app.go","line":12,"body":"stale","position":null}]`))
	}))
	defer githubServer.Close()
	authorizer := &recordingProviderAuthorizer{}
	github, err := NewGitHubProvider(githubServer.URL, githubServer.Client(), authorizer)
	if err != nil {
		t.Fatal(err)
	}
	page, err := github.ListReviewThreads(context.Background(), ProviderContext{
		CredentialReference: "vault://github/token", PermissionGrant: "opaque-least-grant"}, "owner/repo", "3", "")
	if err != nil || len(page.Items) != 1 || !page.Items[0].Outdated || authorizer.provider != "github" ||
		authorizer.reference != "vault://github/token" || authorizer.grant != "opaque-least-grant" {
		t.Fatalf("GitHub normalization = %+v auth=%+v err=%v", page, authorizer, err)
	}

	gitlabServer := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`[{"id":42,"iid":5,"title":"Issue","state":"opened","web_url":"https://gitlab.example/i/5","updated_at":"2026-07-22T10:00:00Z"}]`))
	}))
	defer gitlabServer.Close()
	gitlab, err := NewGitLabProvider(gitlabServer.URL, gitlabServer.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	issues, err := gitlab.ListIssues(context.Background(), ProviderContext{}, "group/repo", "")
	if err != nil || len(issues.Items) != 1 || issues.Items[0].ID != "42" {
		t.Fatalf("GitLab normalization = %+v, %v", issues, err)
	}
	if strings.Contains(strings.Join(os.Environ(), "\n"), "resolved-outside-project") {
		t.Fatal("adapter test credential escaped into the process environment")
	}
}
