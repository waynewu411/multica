package github

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// testDiscoverer builds a Discoverer wired to the given httptest.Server. The
// production code formats URLs against api.github.com; the RoundTripper here
// rewrites the host so the test server receives the request unchanged.
func testDiscoverer(srv *httptest.Server) *Discoverer {
	return &Discoverer{
		client: &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			req.URL.Scheme = "http"
			req.URL.Host = srv.Listener.Addr().String()
			return srv.Client().Transport.RoundTrip(req)
		})},
		token:  "ghp_test",
		owner:  "owner",
		repo:   "repo",
		logger: slog.Default(),
	}
}

func TestFetchOpenIssues_HappyPathSinglePage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("labels"); got != "agent:claude-code" {
			t.Errorf("labels query = %q, want agent:claude-code", got)
		}
		if got := r.URL.Query().Get("state"); got != "open" {
			t.Errorf("state query = %q, want open", got)
		}
		w.Header().Set("ETag", `"abc123"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":1,"number":42,"title":"hello","state":"open","html_url":"https://github.com/owner/repo/issues/42","labels":[{"name":"agent:claude-code"}],"user":{"login":"alice"}}]`)
	}))
	defer srv.Close()

	d := testDiscoverer(srv)
	issues, err := d.FetchOpenIssues(context.Background(), "agent:claude-code")
	if err != nil {
		t.Fatalf("FetchOpenIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	if issues[0].Number != 42 {
		t.Errorf("issue.Number = %d, want 42", issues[0].Number)
	}
	if issues[0].Repo.CloneURL != "https://github.com/owner/repo.git" {
		t.Errorf("CloneURL = %q, want hydrated from owner/repo", issues[0].Repo.CloneURL)
	}
	if d.etag != `"abc123"` {
		t.Errorf("etag not stored: got %q", d.etag)
	}
}

func TestFetchOpenIssues_ETagSurvivesAcrossCalls(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("ETag", `"v1"`)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[]`)
			return
		}
		// Second call must arrive with If-None-Match populated.
		if got := r.Header.Get("If-None-Match"); got != `"v1"` {
			t.Errorf("If-None-Match = %q, want %q on call %d", got, `"v1"`, n)
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	d := testDiscoverer(srv)
	if _, err := d.FetchOpenIssues(context.Background(), "agent:x"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := d.FetchOpenIssues(context.Background(), "agent:x"); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("server saw %d calls, want 2", calls.Load())
	}
}

func TestFetchOpenIssues_NotModifiedReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	d := testDiscoverer(srv)
	d.etag = `"prev"`
	issues, err := d.FetchOpenIssues(context.Background(), "agent:x")
	if err != nil {
		t.Fatalf("got error on 304: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("got %d issues on 304, want 0", len(issues))
	}
}

func TestFetchOpenIssues_PaginatesUntilShortPage(t *testing.T) {
	// 100 issues on page 1 signals "there might be more"; page 2 returns 3.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case "1":
			var parts []string
			for i := 1; i <= 100; i++ {
				parts = append(parts, fmt.Sprintf(`{"id":%d,"number":%d,"state":"open"}`, i, i))
			}
			_, _ = io.WriteString(w, "["+strings.Join(parts, ",")+"]")
		case "2":
			_, _ = io.WriteString(w, `[{"id":101,"number":101,"state":"open"},{"id":102,"number":102,"state":"open"},{"id":103,"number":103,"state":"open"}]`)
		default:
			t.Errorf("unexpected page %q", page)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	d := testDiscoverer(srv)
	issues, err := d.FetchOpenIssues(context.Background(), "agent:x")
	if err != nil {
		t.Fatalf("FetchOpenIssues: %v", err)
	}
	if len(issues) != 103 {
		t.Errorf("got %d issues, want 103 across two pages", len(issues))
	}
}

func TestFetchOpenIssues_RateLimited_CtxCancels(t *testing.T) {
	// fetchPage on 429 blocks for the Retry-After duration (default 60s when
	// the header is missing or unparsable). Use ctx cancellation to assert
	// the select-on-ctx.Done branch fires and unblocks the goroutine.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	d := testDiscoverer(srv)
	ctx, cancel := context.WithCancel(context.Background())
	go cancel()
	_, err := d.FetchOpenIssues(ctx, "agent:x")
	if err == nil {
		t.Fatal("expected error when ctx cancelled during rate-limit wait, got nil")
	}
}

func TestFetchOpenIssues_ServerErrorReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	}))
	defer srv.Close()

	d := testDiscoverer(srv)
	_, err := d.FetchOpenIssues(context.Background(), "agent:x")
	if err == nil {
		t.Fatal("expected error on HTTP 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %v does not mention HTTP 500", err)
	}
}

func TestFetchOpenIssues_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{not-an-array`)
	}))
	defer srv.Close()

	d := testDiscoverer(srv)
	_, err := d.FetchOpenIssues(context.Background(), "agent:x")
	if err == nil {
		t.Fatal("expected error on malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("error %v should mention decode failure", err)
	}
}

func TestFetchOpenIssues_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()

	d := testDiscoverer(srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := d.FetchOpenIssues(ctx, "agent:x")
	if err == nil {
		t.Fatal("expected error after ctx cancel, got nil")
	}
}

func TestFilterIssues_OnlyOpenWithExactLabel(t *testing.T) {
	in := []Issue{
		{Number: 1, State: "open", Labels: []Label{{Name: "agent:claude-code"}}},
		{Number: 2, State: "open", Labels: []Label{{Name: "bug"}}},                          // wrong label
		{Number: 3, State: "open", Labels: []Label{{Name: "agent:claude-code-x"}}},          // prefix collision, must not match
		{Number: 4, State: "closed", Labels: []Label{{Name: "agent:claude-code"}}},          // wrong state
		{Number: 5, State: "open", Labels: []Label{{Name: "x"}, {Name: "agent:claude-code"}}}, // multi-label, has ours
	}
	out := FilterIssues(in, "claude-code")
	if len(out) != 2 {
		t.Fatalf("got %d issues, want 2 (numbers 1 and 5)", len(out))
	}
	if out[0].Number != 1 || out[1].Number != 5 {
		t.Errorf("got issues %v, want [1, 5]", []int{out[0].Number, out[1].Number})
	}
}
