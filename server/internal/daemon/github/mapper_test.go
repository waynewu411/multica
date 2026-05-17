package github

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMapIssueToTask(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "multica")
	agentDir := filepath.Join(cfgDir, "agents", "claude-code")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte("You are a helpful assistant."), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Token: "ghp_test",
		Repos: []string{"owner/testrepo"},
		Agents: map[string]AgentConfig{
			"claude-code": {
				Provider:     "claude_code",
				Model:        "claude-opus-4-7",
				Role:         "coder",
				Instructions: "Write tests.",
			},
		},
	}

	issue := Issue{
		ID:     123456789,
		Number: 42,
		Title:  "Fix login bug",
		Body:   "The login form returns 500 when email contains a + sign.",
		State:  "open",
		Labels: []Label{{Name: "agent:claude-code"}},
	}
	issue.Repo.CloneURL = "https://github.com/owner/testrepo.git"

	task := MapIssueToTask(issue, "claude-code", cfg, cfgDir)

	if task.ID != "gh:123456789" {
		t.Errorf("ID = %q, want gh:123456789", task.ID)
	}
	if task.IssueID != "TES-42" {
		t.Errorf("IssueID = %q, want TES-42", task.IssueID)
	}
	if task.Agent == nil {
		t.Fatal("Agent is nil")
	}
	if task.Agent.Name != "claude-code" {
		t.Errorf("Agent.Name = %q, want claude-code", task.Agent.Name)
	}
	if len(task.Repos) != 1 || task.Repos[0].URL != "https://github.com/owner/testrepo.git" {
		t.Errorf("Repos = %v", task.Repos)
	}
}

func TestMapIssueToTaskInstructionsFromBody(t *testing.T) {
	cfg := &Config{
		Repos: []string{"owner/testrepo"},
		Agents: map[string]AgentConfig{
			"claude-code": {
				Provider:     "claude_code",
				Instructions: "Default instructions.",
			},
		},
	}

	issue := Issue{
		ID:     1,
		Number: 1,
		Title:  "Test",
		Body:   "Some body.\n\n<!-- agent:instructions\nFocus on security above all else.\n-->\n",
		State:  "open",
		Labels: []Label{{Name: "agent:claude-code"}},
	}
	issue.Repo.CloneURL = "https://github.com/owner/testrepo.git"

	task := MapIssueToTask(issue, "claude-code", cfg, "")
	if task.Agent.Instructions != "Focus on security above all else." {
		t.Errorf("Instructions = %q, want issue body instructions", task.Agent.Instructions)
	}
}

func TestRepoToPrefix(t *testing.T) {
	tests := []struct {
		repo string
		want string
	}{
		{"owner/multica", "MUL"},
		{"owner/my-cool-project", "MYC"},
		{"owner/cli", "CLI"},
		{"owner/a", "A"},
		{"test", "TES"},
	}
	for _, tt := range tests {
		got := repoToPrefix(tt.repo)
		if got != tt.want {
			t.Errorf("repoToPrefix(%q) = %q, want %q", tt.repo, got, tt.want)
		}
	}
}

func TestExtractIssueInstructions(t *testing.T) {
	body := "Some text.\n\n<!-- agent:instructions\nWrite secure code.\nUse parameterized queries.\n-->\n\nMore text."
	got := extractIssueInstructions(body)
	want := "Write secure code.\nUse parameterized queries."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// No instructions block.
	if got := extractIssueInstructions("Just a plain body."); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}
