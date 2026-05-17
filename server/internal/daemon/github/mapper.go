package github

import (
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/multica-ai/multica/server/internal/daemon"
)

// MapIssueToTask converts a GitHub Issue to a daemon Task.
func MapIssueToTask(issue Issue, agentName string, cfg *Config, cfgDir string) daemon.Task {
	agent := cfg.Agents[agentName]

	repoPrefix := repoToPrefix(cfg.Repos[0]) // Use first configured repo as base for display prefix

	return daemon.Task{
		ID:      fmt.Sprintf("gh:%d", issue.ID),
		IssueID: fmt.Sprintf("%s-%d", repoPrefix, issue.Number),
		AgentID: agentName,
		Agent: &daemon.AgentData{
			ID:           agentName,
			Name:         agentName,
			Instructions: resolveInstructions(issue.Body, agent.Instructions, agentName, cfgDir),
		},
		Repos: []daemon.RepoData{
			{URL: issue.Repo.CloneURL},
		},
		PriorSessionID: "", // GitHub mode always starts fresh
	}
}

// ReportTask stores the task ID needed for reporting results back.
type ReportTask struct {
	Task   daemon.Task
	Owner  string
	Repo   string
	Number int
}

// NewReportTask creates a ReportTask from a daemon Task.
func NewReportTask(task daemon.Task, owner, repo string, number int) ReportTask {
	return ReportTask{
		Task:   task,
		Owner:  owner,
		Repo:   repo,
		Number: number,
	}
}

func resolveInstructions(issueBody, configInstr, agentName, cfgDir string) string {
	// Issue body <!-- agent:instructions ... --> takes highest priority.
	if instr := extractIssueInstructions(issueBody); instr != "" {
		return instr
	}
	// Fall back to config-level instructions.
	if configInstr != "" {
		return configInstr
	}
	// If AGENTS.md exists on disk, its content is used by the runtime config
	// injection (execenv). Don't duplicate here — just note it exists.
	path := AgentInstructionPath(cfgDir, agentName)
	if _, err := os.Stat(path); err == nil {
		return fmt.Sprintf("See %s for full agent persona.", path)
	}
	return ""
}

func extractIssueInstructions(body string) string {
	const start = "<!-- agent:instructions"
	const end = "-->"
	s := strings.Index(body, start)
	if s < 0 {
		return ""
	}
	s += len(start)
	e := strings.Index(body[s:], end)
	if e < 0 {
		return ""
	}
	return strings.TrimSpace(body[s : s+e])
}

func repoToPrefix(repo string) string {
	// e.g. "owner/multica" → "MUL"
	var name string
	if idx := strings.LastIndex(repo, "/"); idx >= 0 {
		name = repo[idx+1:]
	} else {
		name = repo
	}
	var b strings.Builder
	for _, r := range name {
		if b.Len() >= 3 {
			break
		}
		if unicode.IsLetter(r) {
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}
