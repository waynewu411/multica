package github

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	content := `
daemon:
  poll_interval: "30s"
  max_concurrent_tasks: 3
  orphan_timeout: "30m"
  comment_max_chars: 60000

repos:
  - owner/myrepo

agents:
  claude-code:
    provider: claude_code
    model: claude-opus-4-7
    role: coder
    instructions: "Write tests."
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GITHUB_TOKEN", "ghp_test123")
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Token != "ghp_test123" {
		t.Errorf("Token = %q, want %q", cfg.Token, "ghp_test123")
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0] != "owner/myrepo" {
		t.Errorf("Repos = %v, want [owner/myrepo]", cfg.Repos)
	}
	if _, ok := cfg.Agents["claude-code"]; !ok {
		t.Error("agent claude-code not found")
	}
	if cfg.Daemon.PollInterval != 30*time.Second {
		t.Errorf("PollInterval = %v, want 30s", cfg.Daemon.PollInterval)
	}
}

func TestLoadConfigMissingToken(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	content := `
repos:
  - owner/myrepo

agents:
  claude-code:
    provider: claude_code
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Error("expected error for missing token, got nil")
	}
}

func TestLoadConfigMissingRepos(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	content := `
agents:
  claude-code:
    provider: claude_code
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GITHUB_TOKEN", "ghp_test123")
	_, err := LoadConfig(configPath)
	if err == nil {
		t.Error("expected error for missing repos, got nil")
	}
}

func TestLoadConfigInvalidAgentName(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	content := `
repos:
  - owner/myrepo

agents:
  "bad name!":
    provider: claude_code
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GITHUB_TOKEN", "ghp_test123")
	_, err := LoadConfig(configPath)
	if err == nil {
		t.Error("expected error for invalid agent name, got nil")
	}
}

func TestLoadConfigRejectsAllowedRepoOutsideRepos(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	content := `
repos:
  - owner/repo1

agents:
  claude-code:
    provider: claude
    allowed_repos:
      - owner/repo1
      - owner/repo2   # not in top-level repos — must fail
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GITHUB_TOKEN", "ghp_test123")
	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatal("expected error for allowed_repos entry outside repos, got nil")
	}
	if !strings.Contains(err.Error(), "owner/repo2") {
		t.Errorf("error %v does not mention the offending repo", err)
	}
}

func TestLoadConfigAcceptsAllowedReposSubsetOfRepos(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	content := `
repos:
  - owner/repo1
  - owner/repo2

agents:
  claude-code:
    provider: claude
    allowed_repos:
      - owner/repo1
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GITHUB_TOKEN", "ghp_test123")
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if got := cfg.Agents["claude-code"].AllowedRepos; len(got) != 1 || got[0] != "owner/repo1" {
		t.Errorf("AllowedRepos = %v, want [owner/repo1]", got)
	}
}

func TestLoadConfigGithubTokenFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	tokenPath := filepath.Join(dir, "token.txt")

	content := `
repos:
  - owner/myrepo

agents:
  claude-code:
    provider: claude_code
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("ghp_from_file"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN_FILE", tokenPath)
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Token != "ghp_from_file" {
		t.Errorf("Token = %q, want %q", cfg.Token, "ghp_from_file")
	}
}

func TestPollIntervalForAgentsRepos(t *testing.T) {
	tests := []struct {
		name     string
		agents   int
		repos    int
		cfg      time.Duration
		want     time.Duration
	}{
		// Small deployments stay at the configured interval.
		{"1 agent, 1 repo", 1, 1, 30 * time.Second, 30 * time.Second},
		{"2 agents, 5 repos under floor", 2, 5, 30 * time.Second, 30 * time.Second},
		// Big fan-out pushes the floor up. 3 agents × 20 repos × 1 page = 60 cycle reqs
		// against 4000 req/h budget → minInterval = 60h/4000 = 54s.
		{"3 agents, 20 repos forces floor", 3, 20, 30 * time.Second, 60 * time.Hour / 4000},
		// User asked for a slower interval than the floor — honor the slower one.
		{"slow configured wins", 2, 5, 5 * time.Minute, 5 * time.Minute},
		// Degenerate inputs fall back to configured.
		{"zero agents", 0, 5, 30 * time.Second, 30 * time.Second},
		{"zero repos", 2, 0, 30 * time.Second, 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pollIntervalForAgentsRepos(tt.agents, tt.repos, tt.cfg); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	d, err := parseDuration("5m", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if d != 5*time.Minute {
		t.Errorf("got %v, want 5m", d)
	}

	d, err = parseDuration("", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if d != 30*time.Second {
		t.Errorf("got %v, want 30s", d)
	}

	_, err = parseDuration("-1s", 30*time.Second)
	if err == nil {
		t.Error("expected error for negative duration")
	}
}
