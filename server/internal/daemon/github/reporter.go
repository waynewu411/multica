package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Reporter writes task results to GitHub Issues.
type Reporter struct {
	client  *http.Client
	token   string
	owner   string
	repo    string
	maxBody int
	logger  *slog.Logger
}

// NewReporter creates a new Reporter.
func NewReporter(token, owner, repo string, maxBody int, logger *slog.Logger) *Reporter {
	return &Reporter{
		client:  &http.Client{Timeout: 30 * time.Second},
		token:   token,
		owner:   owner,
		repo:    repo,
		maxBody: maxBody,
		logger:  logger,
	}
}

// ReportResult posts the agent's result as an issue comment.
func (r *Reporter) ReportResult(ctx context.Context, issueNumber int, output string, success bool) error {
	title := "Task completed"
	if !success {
		title = "Task failed"
	}

	body := fmt.Sprintf("## %s\n\n%s", title, truncate(output, r.maxBody))
	return r.postComment(ctx, issueNumber, body)
}

// PostClaimComment posts a "working on this" comment to signal claim.
func (r *Reporter) PostClaimComment(ctx context.Context, issueNumber int, agentName string) error {
	body := fmt.Sprintf("%s is working on this...", agentName)
	return r.postComment(ctx, issueNumber, body)
}

// RemoveLabel removes a single label from the issue. A 404 from GitHub is
// treated as success — the label was already removed (e.g., by a concurrent
// daemon racing on the same issue, or by a human cleaning up).
func (r *Reporter) RemoveLabel(ctx context.Context, issueNumber int, label string) error {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/labels/%s",
		r.owner, r.repo, issueNumber, url.PathEscape(label))

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("remove label: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// CloseIssue closes the GitHub issue.
func (r *Reporter) CloseIssue(ctx context.Context, issueNumber int) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d",
		r.owner, r.repo, issueNumber)

	payload := []byte(`{"state":"closed"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("close issue: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (r *Reporter) postComment(ctx context.Context, issueNumber int, body string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/comments",
		r.owner, r.repo, issueNumber)

	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("post comment: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-100] + "\n\n[... output truncated, see agent logs on local machine for full output]"
}
