package executor

import (
	"testing"
)

func TestRescueRefFromThought(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     map[string]interface{}
		thought  string
		wantRef  string
	}{
		{
			name:     "already has ref",
			toolName: "git_show",
			args:     map[string]interface{}{"ref": "abc1234"},
			thought:  "inspecting commit def5678",
			wantRef:  "abc1234",
		},
		{
			name:     "extract hex hash from thought",
			toolName: "git_show",
			args:     map[string]interface{}{},
			thought:  "I should inspect commit a1b2c3d4e5f6 to see the changes",
			wantRef:  "a1b2c3d4e5f6",
		},
		{
			name:     "extract diff against branch",
			toolName: "git_diff",
			args:     map[string]interface{}{},
			thought:  "Let's diff against main to see unmerged edits",
			wantRef:  "main",
		},
		{
			name:     "extract changes since tag",
			toolName: "git_diff",
			args:     map[string]interface{}{},
			thought:  "Check changes since v1.0.0",
			wantRef:  "v1.0.0",
		},
		{
			name:     "git_show defaults to HEAD when empty",
			toolName: "git_show",
			args:     map[string]interface{}{},
			thought:  "Show latest commit",
			wantRef:  "HEAD",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rescueRefFromThought(tt.toolName, tt.args, tt.thought)
			if ref, _ := got["ref"].(string); ref != tt.wantRef {
				t.Errorf("rescueRefFromThought() ref = %q, want %q", ref, tt.wantRef)
			}
		})
	}
}

func TestRescueMaxCountFromThought(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		args      map[string]interface{}
		thought   string
		wantCount int
	}{
		{
			name:      "already has maxCount",
			toolName:  "git_log",
			args:      map[string]interface{}{"maxCount": 15},
			thought:   "look at last 5 commits",
			wantCount: 15,
		},
		{
			name:      "extract last 5 commits",
			toolName:  "git_log",
			args:      map[string]interface{}{},
			thought:   "Check the last 5 commits for compiler fixes",
			wantCount: 5,
		},
		{
			name:      "extract recent 10",
			toolName:  "git_log",
			args:      map[string]interface{}{},
			thought:   "Show recent 10 log entries",
			wantCount: 10,
		},
		{
			name:      "extract past 20",
			toolName:  "git_log",
			args:      map[string]interface{}{},
			thought:   "Explore past 20 commits in repo",
			wantCount: 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rescueMaxCountFromThought(tt.toolName, tt.args, tt.thought)
			count, _ := got["maxCount"].(int)
			if count != tt.wantCount {
				t.Errorf("rescueMaxCountFromThought() maxCount = %v, want %v", got["maxCount"], tt.wantCount)
			}
		})
	}
}

func TestRescueFileGlobFromThought(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     map[string]interface{}
		thought  string
		wantGlob string
	}{
		{
			name:     "already has fileGlob",
			toolName: "search_files",
			args:     map[string]interface{}{"fileGlob": "*.go"},
			thought:  "search in .md files",
			wantGlob: "*.go",
		},
		{
			name:     "extract explicit *.ts glob",
			toolName: "search_files",
			args:     map[string]interface{}{},
			thought:  "Search for handler in *.ts files",
			wantGlob: "*.ts",
		},
		{
			name:     "extract .md extension",
			toolName: "search_files",
			args:     map[string]interface{}{},
			thought:  "search in .md files for architecture",
			wantGlob: "*.md",
		},
		{
			name:     "extract Go files natural language",
			toolName: "search_files",
			args:     map[string]interface{}{},
			thought:  "Search only Go files for NewSearchFilesTool",
			wantGlob: "*.go",
		},
		{
			name:     "extract Python files natural language",
			toolName: "search_files",
			args:     map[string]interface{}{},
			thought:  "look in python files for model class",
			wantGlob: "*.py",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rescueFileGlobFromThought(tt.toolName, tt.args, tt.thought)
			if glob, _ := got["fileGlob"].(string); glob != tt.wantGlob {
				t.Errorf("rescueFileGlobFromThought() fileGlob = %q, want %q", glob, tt.wantGlob)
			}
		})
	}
}

func TestRescueEmptyPathFromThought_GitTools(t *testing.T) {
	args := rescueEmptyPathFromThought("git_log", map[string]interface{}{}, "Check internal/compiler git log")
	path, _ := args["path"].(string)
	if path == "" {
		t.Errorf("expected path to be rescued for git_log, got empty")
	}

	diffArgs := rescueEmptyPathFromThought("git_diff", map[string]interface{}{}, "Check changes in internal/tools")
	diffPath, _ := diffArgs["path"].(string)
	if diffPath == "" {
		t.Errorf("expected path to be rescued for git_diff, got empty")
	}
}

func TestClassifyProbeGoal_GitKeywords(t *testing.T) {
	gitGoals := []string{
		"Examine commit history of internal/executor",
		"Check git log for recent fixes",
		"Analyze regression in test runner",
		"Find who changed probe execution logic",
		"See what changed between versions",
		"Track evolution of Kahn compiler",
		"Analyze improvement arc across last 30 commits",
	}

	for _, g := range gitGoals {
		mode := classifyProbeGoal(g)
		if mode != "focused" {
			t.Errorf("classifyProbeGoal(%q) = %q, want 'focused'", g, mode)
		}
	}
}
