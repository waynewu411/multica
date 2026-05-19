package github

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultPollInterval  = 30 * time.Second
	DefaultOrphanTimeout = 30 * time.Minute
	DefaultCommentMax    = 60000
)

// Config holds GitHub-mode daemon configuration.
type Config struct {
	Token  string
	Repos  []string
	Agents map[string]AgentConfig
	Daemon DaemonConfig
}

// AgentConfig defines one agent entry.
type AgentConfig struct {
	Provider     string   `yaml:"provider"`
	Model        string   `yaml:"model"`
	Role         string   `yaml:"role"`
	Instructions string   `yaml:"instructions"`
	AllowedRepos []string `yaml:"allowed_repos"`
}

// DaemonConfig holds runtime parameters.
type DaemonConfig struct {
	PollInterval    time.Duration
	MaxConcurrent   int
	OrphanTimeout   time.Duration
	CommentMaxChars int
}

// rawConfig mirrors the YAML file structure.
type rawConfig struct {
	Daemon struct {
		PollInterval    string `yaml:"poll_interval"`
		MaxConcurrent   int    `yaml:"max_concurrent_tasks"`
		OrphanTimeout   string `yaml:"orphan_timeout"`
		CommentMaxChars int    `yaml:"comment_max_chars"`
	} `yaml:"daemon"`
	Repos  []string                `yaml:"repos"`
	Agents map[string]AgentConfig  `yaml:"agents"`
}

// LoadConfig reads and validates the GitHub-mode config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		if tokenFile := strings.TrimSpace(os.Getenv("GITHUB_TOKEN_FILE")); tokenFile != "" {
			tokenBytes, err := os.ReadFile(tokenFile)
			if err != nil {
				return nil, fmt.Errorf("read GITHUB_TOKEN_FILE %s: %w", tokenFile, err)
			}
			token = strings.TrimSpace(string(tokenBytes))
		}
	}
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN environment variable is required")
	}

	if len(raw.Repos) == 0 {
		return nil, fmt.Errorf("at least one repo is required in config")
	}

	if len(raw.Agents) == 0 {
		return nil, fmt.Errorf("at least one agent is required in config")
	}

	// Validate agent names — they must match the label values used on issues.
	for name := range raw.Agents {
		if !isSafeLabelValue(name) {
			return nil, fmt.Errorf("invalid agent name %q: must be alphanumeric with hyphens only", name)
		}
	}

	// Validate that every entry in AllowedRepos for every agent is also in
	// the top-level repos list. An entry outside the configured set is
	// almost certainly a typo, and silently ignoring it would weaken the
	// security boundary AllowedRepos exists to enforce. Fail at startup
	// rather than at first surprising poll.
	repoSet := make(map[string]struct{}, len(raw.Repos))
	for _, r := range raw.Repos {
		repoSet[r] = struct{}{}
	}
	for name, ag := range raw.Agents {
		for _, allowed := range ag.AllowedRepos {
			if _, ok := repoSet[allowed]; !ok {
				return nil, fmt.Errorf("agent %q: allowed_repos entry %q is not in the top-level repos list", name, allowed)
			}
		}
	}

	pollInterval, err := parseDuration(raw.Daemon.PollInterval, DefaultPollInterval)
	if err != nil {
		return nil, fmt.Errorf("daemon.poll_interval: %w", err)
	}

	orphanTimeout, err := parseDuration(raw.Daemon.OrphanTimeout, DefaultOrphanTimeout)
	if err != nil {
		return nil, fmt.Errorf("daemon.orphan_timeout: %w", err)
	}

	maxConcurrent := raw.Daemon.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}

	commentMax := raw.Daemon.CommentMaxChars
	if commentMax <= 0 {
		commentMax = DefaultCommentMax
	}

	// Compute effective poll interval based on repo count and rate limit safety.
	effectivePoll := pollIntervalForRepos(len(raw.Repos), pollInterval)

	return &Config{
		Token:  token,
		Repos:  raw.Repos,
		Agents: raw.Agents,
		Daemon: DaemonConfig{
			PollInterval:    effectivePoll,
			MaxConcurrent:   maxConcurrent,
			OrphanTimeout:   orphanTimeout,
			CommentMaxChars: commentMax,
		},
	}, nil
}

// AgentInstructionPath returns the expected AGENTS.md path for an agent.
func AgentInstructionPath(cfgDir, agentName string) string {
	return filepath.Join(cfgDir, "agents", agentName, "AGENTS.md")
}

// pollIntervalForRepos ensures poll rate stays within 80% of GitHub's 5000 req/h limit.
func pollIntervalForRepos(numRepos int, configured time.Duration) time.Duration {
	const maxReqPerHour = 4000 // 80% of 5000
	// Each poll cycle does one request per repo.
	minInterval := time.Duration(numRepos) * time.Hour / time.Duration(maxReqPerHour)
	if minInterval < configured {
		minInterval = configured
	}
	return minInterval
}

func parseDuration(raw string, def time.Duration) (time.Duration, error) {
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return d, nil
}

func isSafeLabelValue(s string) bool {
	if s == "" || len(s) > 50 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
	}
	return true
}
