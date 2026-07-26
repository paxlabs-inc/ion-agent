package activation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type testSemanticSource struct {
	mu    sync.Mutex
	limit int
	err   error
}

func (source *testSemanticSource) SearchSemantic(
	_ context.Context,
	_ string,
	limit int,
) ([]string, error) {
	source.mu.Lock()
	source.limit = limit
	source.mu.Unlock()
	return []string{"semantic memory"}, source.err
}

type testFTS5Source struct {
	mu      sync.Mutex
	request Request
	limit   int
	err     error
}

func (source *testFTS5Source) SearchFTS5(
	_ context.Context,
	request Request,
	limit int,
) ([]string, error) {
	source.mu.Lock()
	source.request = request
	source.limit = limit
	source.mu.Unlock()
	return []string{"timeline memory"}, source.err
}

type testSalienceSource struct{ err error }

func (source testSalienceSource) ScanSalience(
	context.Context,
	string,
	int,
) ([]string, error) {
	return []string{"salient memory"}, source.err
}

type testPremiseSource struct{ err error }

func (source testPremiseSource) RelevantPremises(
	context.Context,
	string,
) ([]string, error) {
	return []string{"grounded premise"}, source.err
}

func TestPipelineExecutesAllFourRequiredBranches(t *testing.T) {
	t.Parallel()
	semantic := &testSemanticSource{}
	fts := &testFTS5Source{}
	pipeline, err := NewPipeline(
		semantic,
		fts,
		testSalienceSource{},
		testPremiseSource{},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Query: "deployment", UserID: "user-a", SessionID: "session-a",
	}
	result, err := pipeline.Prefetch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(result.HNSWResults) != "[semantic memory]" ||
		fmt.Sprint(result.FTS5Results) != "[timeline memory]" ||
		fmt.Sprint(result.SalienceHits) != "[salient memory]" ||
		fmt.Sprint(result.PremiseRefs) != "[grounded premise]" {
		t.Fatalf("prefetch result = %+v", result)
	}
	semantic.mu.Lock()
	semanticLimit := semantic.limit
	semantic.mu.Unlock()
	fts.mu.Lock()
	ftsLimit, ftsRequest := fts.limit, fts.request
	fts.mu.Unlock()
	if semanticLimit != 10 || ftsLimit != 10 || ftsRequest != request {
		t.Fatalf(
			"semantic limit = %d, FTS limit = %d, request = %+v",
			semanticLimit,
			ftsLimit,
			ftsRequest,
		)
	}
}

func TestPipelineFailsClosedWhenAnyBranchFails(t *testing.T) {
	t.Parallel()
	pipeline, err := NewPipeline(
		&testSemanticSource{},
		&testFTS5Source{},
		testSalienceSource{err: errors.New("salience unavailable")},
		testPremiseSource{},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pipeline.Prefetch(context.Background(), Request{
		Query: "query", UserID: "user", SessionID: "session",
	})
	if err == nil || !strings.Contains(err.Error(), "salience unavailable") {
		t.Fatalf("Prefetch() error = %v", err)
	}
}

type staticPrefetcher struct{}

func (staticPrefetcher) Prefetch(
	context.Context,
	Request,
) (*PrefetchResult, error) {
	return &PrefetchResult{
		HNSWResults:  []string{"semantic"},
		FTS5Results:  []string{"timeline"},
		SalienceHits: []string{"salient"},
		PremiseRefs:  []string{"premise"},
	}, nil
}

type allTierSource struct{}

func (allTierSource) Entries(context.Context, Request) ([]Entry, error) {
	return []Entry{
		{Tier: TierPinned, Content: "identity", Salience: 1},
		{Tier: TierTimeline, Content: "epoch", Salience: 0.8},
		{Tier: TierDreams, Content: "dream", Salience: 0.7},
		{Tier: TierRecent, Content: "hour", Salience: 0.6},
		{Tier: TierTranscript, Content: "user: current", Salience: 0.5},
	}, nil
}

func TestServiceComposesFourTiersAndDreamInsertionPoint(t *testing.T) {
	t.Parallel()
	service, err := NewService(32000, staticPrefetcher{}, allTierSource{})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := service.Activate(
		context.Background(),
		"query",
		"user",
		"session",
	)
	if err != nil {
		t.Fatal(err)
	}
	previous := -1
	for _, header := range []string{
		"## pinned", "## timeline", "## dreams", "## recent", "## transcript",
	} {
		index := strings.Index(rendered, header)
		if index <= previous {
			t.Fatalf("tier %q is absent or out of order:\n%s", header, rendered)
		}
		previous = index
	}
	for _, content := range []string{
		"premise", "timeline", "semantic", "salient", "user: current",
	} {
		if !strings.Contains(rendered, content) {
			t.Fatalf("activation does not contain %q:\n%s", content, rendered)
		}
	}
}
