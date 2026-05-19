package github

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRemoveLabel(t *testing.T) {
	var (
		gotMethod atomic.Value
		gotPath   atomic.Value
		gotAuth   atomic.Value
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod.Store(r.Method)
		gotPath.Store(r.URL.Path)
		gotAuth.Store(r.Header.Get("Authorization"))
		// Github returns 200 with the remaining labels on success.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()

	r := &Reporter{
		client: srv.Client(),
		token:  "ghp_test",
		owner:  "owner",
		repo:   "repo",
		logger: slog.Default(),
	}
	// Point requests at the test server. Override the base via a thin shim:
	// the production code formats the URL itself, so we replace the host by
	// rewriting through a custom RoundTripper.
	r.client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = srv.Listener.Addr().String()
		return srv.Client().Transport.RoundTrip(req)
	})}

	removed, err := r.RemoveLabel(context.Background(), 42, "agent:claude-code")
	if err != nil {
		t.Fatalf("RemoveLabel returned error: %v", err)
	}
	if !removed {
		t.Error("removed = false on HTTP 200, want true")
	}

	if m := gotMethod.Load().(string); m != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", m)
	}
	wantPath := "/repos/owner/repo/issues/42/labels/agent:claude-code"
	if p := gotPath.Load().(string); p != wantPath {
		t.Errorf("path = %q, want %q", p, wantPath)
	}
	if a := gotAuth.Load().(string); !strings.HasPrefix(a, "Bearer ") {
		t.Errorf("authorization header missing or wrong: %q", a)
	}
}

func TestRemoveLabelNotFoundReportsNotClaimed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Label does not exist"}`)
	}))
	defer srv.Close()

	r := &Reporter{
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

	removed, err := r.RemoveLabel(context.Background(), 42, "agent:claude-code")
	if err != nil {
		t.Fatalf("RemoveLabel returned error for 404, want nil: %v", err)
	}
	if removed {
		t.Error("removed = true on HTTP 404, want false (lock not held)")
	}
}

func TestRemoveLabelServerErrorReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"insufficient scopes"}`)
	}))
	defer srv.Close()

	r := &Reporter{
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

	removed, err := r.RemoveLabel(context.Background(), 42, "agent:claude-code")
	if err == nil {
		t.Fatal("RemoveLabel returned nil for 403, want error")
	}
	if removed {
		t.Error("removed = true on error, want false")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %v, want it to mention HTTP 403", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
