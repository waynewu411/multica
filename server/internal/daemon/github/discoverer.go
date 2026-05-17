package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Issue represents a minimal GitHub Issue used by the discoverer.
type Issue struct {
	ID     int64  `json:"id"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	URL    string `json:"html_url"`
	Repo   struct {
		CloneURL string `json:"clone_url"`
	} `json:"-"`
	Labels []Label `json:"labels"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
}

// Label represents a GitHub label.
type Label struct {
	Name string `json:"name"`
}

// Discoverer polls the GitHub Issues API for tasks.
type Discoverer struct {
	client  *http.Client
	token   string
	owner   string
	repo    string
	etag    string
	logger  *slog.Logger
}

// NewDiscoverer creates a new Discoverer for a given repo.
func NewDiscoverer(token, owner, repo string, logger *slog.Logger) *Discoverer {
	return &Discoverer{
		client: &http.Client{Timeout: 30 * time.Second},
		token:  token,
		owner:  owner,
		repo:   repo,
		logger: logger,
	}
}

// RepoName returns the full owner/repo string.
func (d *Discoverer) RepoName() string {
	return d.owner + "/" + d.repo
}

// FetchOpenIssues gets all open issues with the given label set. It handles
// pagination via Link headers and respects rate limits.
func (d *Discoverer) FetchOpenIssues(ctx context.Context, labelFilter string) ([]Issue, error) {
	var all []Issue
	page := 1

	for {
		issues, hasMore, err := d.fetchPage(ctx, labelFilter, page)
		if err != nil {
			return nil, fmt.Errorf("fetch page %d: %w", page, err)
		}
		all = append(all, issues...)
		if !hasMore {
			break
		}
		page++
	}

	return all, nil
}

func (d *Discoverer) fetchPage(ctx context.Context, labelFilter string, page int) ([]Issue, bool, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues?labels=%s&state=open&per_page=100&page=%d",
		d.owner, d.repo, labelFilter, page)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+d.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if d.etag != "" {
		req.Header.Set("If-None-Match", d.etag)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	// Track rate limit state.
	d.trackRateLimit(resp)

	// 304 Not Modified — no changes since last poll.
	if resp.StatusCode == http.StatusNotModified {
		return nil, false, nil
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := resp.Header.Get("Retry-After")
		wait := 60 * time.Second
		if s, err := strconv.Atoi(retryAfter); err == nil && s > 0 {
			wait = time.Duration(s) * time.Second
		}
		d.logger.Warn("rate limited", "repo", d.RepoName(), "retry_after", wait)
		select {
		case <-time.After(wait):
			return nil, false, nil
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, false, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Store ETag for conditional requests.
	if etag := resp.Header.Get("ETag"); etag != "" {
		d.etag = etag
	}

	var issues []Issue
	if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
		return nil, false, fmt.Errorf("decode: %w", err)
	}

	// Hydrate repo clone URL from the first issue.
	for i := range issues {
		issues[i].Repo.CloneURL = fmt.Sprintf("https://github.com/%s/%s.git", d.owner, d.repo)
	}

	hasMore := len(issues) == 100
	return issues, hasMore, nil
}

func (d *Discoverer) trackRateLimit(resp *http.Response) {
	remaining := resp.Header.Get("X-RateLimit-Remaining")
	if remaining == "" {
		return
	}
	n, err := strconv.Atoi(remaining)
	if err != nil {
		return
	}
	if n < 100 {
		d.logger.Warn("rate limit low", "remaining", n, "repo", d.RepoName())
	}
}

// FilterIssues returns issues that need action: not claimed, not done.
func FilterIssues(issues []Issue, agentName string) []Issue {
	agentLabel := "agent:" + agentName
	var out []Issue
	for _, iss := range issues {
		if iss.State != "open" {
			continue
		}
		// Only match issues with our agent label.
		hasLabel := false
		for _, l := range iss.Labels {
			if l.Name == agentLabel {
				hasLabel = true
				break
			}
		}
		if !hasLabel {
			continue
		}
		out = append(out, iss)
	}
	return out
}

// RoundTripWithRetry wraps an HTTP round-trip with exponential backoff.
func RoundTripWithRetry(ctx context.Context, fn func() (*http.Response, error), logger *slog.Logger) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := fn()
		if err == nil {
			if resp.StatusCode < 500 {
				return resp, nil
			}
			resp.Body.Close()
			lastErr = fmt.Errorf("server error %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		if attempt == 2 {
			break
		}
		backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
		logger.Debug("retrying", "attempt", attempt+1, "backoff", backoff, "error", err)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}
