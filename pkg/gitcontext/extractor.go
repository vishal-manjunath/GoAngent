package gitcontext

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Data Structures
type Commit struct {
	SHA       string    `json:"sha"`
	Message   string    `json:"message"`
	Author    string    `json:"author"`
	Timestamp time.Time `json:"timestamp"`
}

type GitContext struct {
	RepoURL       string   `json:"repo_url"`
	Branch        string   `json:"branch"`
	RecentCommits []Commit `json:"recent_commits"`
	ChangedFiles  []string `json:"changed_files"`
	Diff          string   `json:"diff"`
}

type GitConfig struct {
	UserName  string `json:"user_name"`
	UserEmail string `json:"user_email"`
}

type GitContextPayload struct {
	GitContext GitContext `json:"git_context"`
	GitConfig  GitConfig  `json:"git_config"`
}

// Internal runner with timeout and safety
func runGit(repoPath string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// Extract implements the core logic
func Extract(repoPath string) (*GitContextPayload, error) {
	if repoPath == "" {
		cwd, _ := os.Getwd()
		repoPath = cwd
	}

	// 1. Get Repo URL and Branch [cite: 138, 139]
	url, _ := runGit(repoPath, "config", "--get", "remote.origin.url")
	branch, _ := runGit(repoPath, "rev-parse", "--abbrev-ref", "HEAD")

	// 2. Get User Config [cite: 143, 144]
	uName, _ := runGit(repoPath, "config", "user.name")
	uEmail, _ := runGit(repoPath, "config", "user.email")

	// 3. Get Recent Commits [cite: 140]
	// Format: hash|subject|email|ISO-timestamp
	logData, err := runGit(repoPath, "log", "-n", "5", "--pretty=format:%h|%s|%ae|%aI")
	var commits []Commit
	if err == nil && logData != "" {
		lines := strings.Split(logData, "\n")
		for _, line := range lines {
			parts := strings.Split(line, "|")
			if len(parts) == 4 {
				t, _ := time.Parse(time.RFC3339, parts[3]) // [cite: 192]
				commits = append(commits, Commit{
					SHA:       parts[0],
					Message:   parts[1],
					Author:    parts[2],
					Timestamp: t,
				})
			}
		}
	}

	// 4. Get Changed Files [cite: 141]
	filesData, _ := runGit(repoPath, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD")
	changedFiles := strings.Split(filesData, "\n")
	if filesData == "" {
		changedFiles = []string{}
	}

	// 5. Get Diff and handle truncation [cite: 142, 196]
	diff, _ := runGit(repoPath, "show", "HEAD", "--format=", "--unified=3")
	if len(diff) > 50*1024 {
		diff = diff[:50*1024] + "\n... [truncated]"
	}

	return &GitContextPayload{
		GitContext: GitContext{
			RepoURL:       url,
			Branch:        branch,
			RecentCommits: commits,
			ChangedFiles:  changedFiles,
			Diff:          diff,
		},
		GitConfig: GitConfig{
			UserName:  uName,
			UserEmail: uEmail,
		},
	}, nil
}
