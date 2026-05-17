package github

import (
	"os"
	"path/filepath"
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

func TestPollIntervalForRepos(t *testing.T) {
	// 1 repo @ 30s → stays at 30s
	if got := pollIntervalForRepos(1, 30*time.Second); got != 30*time.Second {
		t.Errorf("1 repo: got %v, want 30s", got)
	}
	// 50 repos: min = 50 * 3600 / 4000 = 45s, so 30s → 45s
	want := 50 * time.Hour / 4000
	if got := pollIntervalForRepos(50, 30*time.Second); got != want {
		t.Errorf("50 repos: got %v, want %v", got, want)
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
