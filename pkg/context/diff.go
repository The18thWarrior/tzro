package context

import (
	"bufio"
	"bytes"
	stdctx "context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// GitScope represents the scope of changes to analyze.
type GitScope string

const (
	GitScopeStaged   GitScope = "staged"
	GitScopeUnstaged GitScope = "unstaged"
	GitScopeAll      GitScope = "all"
)

// EmptyTreeHash is Git's standard SHA-1 for an empty tree object.
const EmptyTreeHash = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// DiffLine represents an added, deleted, or context line in a hunk.
type DiffLine struct {
	Type    rune // '+', '-', ' '
	Content string
	OldLine int
	NewLine int
}

// DiffHunk represents a unified diff hunk.
type DiffHunk struct {
	OldStartLine int
	OldLineCount int
	NewStartLine int
	NewLineCount int
	Header       string
	DeletedLines []int
	AddedLines   []int
	Lines        []DiffLine
}

// GitDiffFile represents a single modified, added, renamed, or deleted file in a diff.
type GitDiffFile struct {
	OldPath   string
	NewPath   string
	IsBinary  bool
	IsDeleted bool
	IsNew     bool
	IsRename  bool
	OldSource []byte
	NewSource []byte
	Hunks     []DiffHunk
}

// GitDiffResult holds the complete acquired diff snapshot.
type GitDiffResult struct {
	Scope          GitScope
	RawDiff        string
	Files          []GitDiffFile
	UntrackedFiles []string
	UnbornHEAD     bool
}

// AcquireGitDiff runs git diff commands with safe argument arrays and retrieves before/after snapshots.
func AcquireGitDiff(ctx stdctx.Context, workDir string, scope GitScope) (*GitDiffResult, error) {
	if scope != GitScopeStaged && scope != GitScopeUnstaged && scope != GitScopeAll {
		return nil, fmt.Errorf("invalid git scope %q (must be staged, unstaged, or all)", scope)
	}

	// Verify we are inside a git repository
	chkCmd := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	chkCmd.Dir = workDir
	if err := chkCmd.Run(); err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}

	// Check if HEAD exists (unborn HEAD)
	headCmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "HEAD")
	headCmd.Dir = workDir
	unbornHEAD := false
	if err := headCmd.Run(); err != nil {
		unbornHEAD = true
	}

	var gitArgs []string
	switch scope {
	case GitScopeStaged:
		if unbornHEAD {
			gitArgs = []string{"diff", "--cached", EmptyTreeHash}
		} else {
			gitArgs = []string{"diff", "--cached"}
		}
	case GitScopeUnstaged:
		gitArgs = []string{"diff"}
	case GitScopeAll:
		if unbornHEAD {
			gitArgs = []string{"diff"}
		} else {
			gitArgs = []string{"diff", "HEAD"}
		}
	}

	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	cmd.Dir = workDir
	diffBytes, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s failed: %w", strings.Join(gitArgs, " "), err)
	}
	rawDiff := string(diffBytes)

	res := &GitDiffResult{
		Scope:      scope,
		RawDiff:    rawDiff,
		UnbornHEAD: unbornHEAD,
	}

	// For --all, also enumerate untracked files
	if scope == GitScopeAll {
		untrackedCmd := exec.CommandContext(ctx, "git", "ls-files", "--others", "--exclude-standard", "-z")
		untrackedCmd.Dir = workDir
		if out, err := untrackedCmd.Output(); err == nil && len(out) > 0 {
			parts := bytes.Split(out, []byte{0})
			for _, p := range parts {
				if len(p) > 0 {
					res.UntrackedFiles = append(res.UntrackedFiles, string(p))
				}
			}
		}
	}

	files := parseUnifiedDiff(rawDiff)

	// Fetch snapshots for each file
	for i := range files {
		f := &files[i]
		// Fetch OldSource
		if !f.IsNew && f.OldPath != "" {
			var showRef string
			if scope == GitScopeStaged || scope == GitScopeAll {
				if !unbornHEAD {
					showRef = fmt.Sprintf("HEAD:%s", f.OldPath)
				}
			} else { // unstaged
				showRef = fmt.Sprintf(":%s", f.OldPath)
			}

			if showRef != "" {
				shCmd := exec.CommandContext(ctx, "git", "show", showRef)
				shCmd.Dir = workDir
				if out, err := shCmd.Output(); err == nil {
					f.OldSource = out
				}
			}
		}

		// Fetch NewSource
		if !f.IsDeleted && f.NewPath != "" {
			if scope == GitScopeStaged {
				// Parse from index blob, never unstaged worktree file!
				shCmd := exec.CommandContext(ctx, "git", "show", fmt.Sprintf(":%s", f.NewPath))
				shCmd.Dir = workDir
				if out, err := shCmd.Output(); err == nil {
					f.NewSource = out
				}
			} else {
				// Unstaged or All: read from live file on disk
				fullPath := filepath.Join(workDir, f.NewPath)
				if content, err := os.ReadFile(fullPath); err == nil {
					f.NewSource = content
				}
			}
		}
	}

	res.Files = files
	return res, nil
}

// parseUnifiedDiff parses a raw unified git diff into structured files and hunks.
func parseUnifiedDiff(diffText string) []GitDiffFile {
	var files []GitDiffFile
	if strings.TrimSpace(diffText) == "" {
		return files
	}

	scanner := bufio.NewScanner(strings.NewReader(diffText))
	var curFile *GitDiffFile
	var curHunk *DiffHunk

	hunkRe := regexp.MustCompile(`^@@\s+-([0-9]+)(?:,([0-9]+))?\s+\+([0-9]+)(?:,([0-9]+))?\s+@@(.*)$`)

	var curOldLine, curNewLine int

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "diff --git ") {
			if curFile != nil {
				if curHunk != nil {
					curFile.Hunks = append(curFile.Hunks, *curHunk)
					curHunk = nil
				}
				files = append(files, *curFile)
			}
			curFile = &GitDiffFile{}
			// Extract paths: diff --git a/path b/path
			parts := strings.Split(line, " ")
			if len(parts) >= 4 {
				curFile.OldPath = strings.TrimPrefix(unquoteGitPath(parts[2]), "a/")
				curFile.NewPath = strings.TrimPrefix(unquoteGitPath(parts[3]), "b/")
			}
			continue
		}

		if curFile == nil {
			continue
		}

		if strings.HasPrefix(line, "new file mode") {
			curFile.IsNew = true
			continue
		}
		if strings.HasPrefix(line, "deleted file mode") {
			curFile.IsDeleted = true
			continue
		}
		if strings.HasPrefix(line, "rename from ") {
			curFile.IsRename = true
			curFile.OldPath = unquoteGitPath(strings.TrimPrefix(line, "rename from "))
			continue
		}
		if strings.HasPrefix(line, "rename to ") {
			curFile.IsRename = true
			curFile.NewPath = unquoteGitPath(strings.TrimPrefix(line, "rename to "))
			continue
		}
		if strings.HasPrefix(line, "Binary files ") {
			curFile.IsBinary = true
			continue
		}
		if strings.HasPrefix(line, "--- a/") {
			curFile.OldPath = unquoteGitPath(strings.TrimPrefix(line, "--- a/"))
			continue
		}
		if strings.HasPrefix(line, "+++ b/") {
			curFile.NewPath = unquoteGitPath(strings.TrimPrefix(line, "+++ b/"))
			continue
		}

		matches := hunkRe.FindStringSubmatch(line)
		if len(matches) > 0 {
			if curHunk != nil {
				curFile.Hunks = append(curFile.Hunks, *curHunk)
			}
			oldStart, _ := strconv.Atoi(matches[1])
			oldCount := 1
			if matches[2] != "" {
				oldCount, _ = strconv.Atoi(matches[2])
			}
			newStart, _ := strconv.Atoi(matches[3])
			newCount := 1
			if matches[4] != "" {
				newCount, _ = strconv.Atoi(matches[4])
			}
			curHunk = &DiffHunk{
				OldStartLine: oldStart,
				OldLineCount: oldCount,
				NewStartLine: newStart,
				NewLineCount: newCount,
				Header:       line,
			}
			curOldLine = oldStart
			curNewLine = newStart
			continue
		}

		if curHunk != nil {
			if len(line) > 0 {
				r := rune(line[0])
				content := line[1:]
				switch r {
				case '+':
					curHunk.AddedLines = append(curHunk.AddedLines, curNewLine)
					curHunk.Lines = append(curHunk.Lines, DiffLine{Type: '+', Content: content, NewLine: curNewLine})
					curNewLine++
				case '-':
					curHunk.DeletedLines = append(curHunk.DeletedLines, curOldLine)
					curHunk.Lines = append(curHunk.Lines, DiffLine{Type: '-', Content: content, OldLine: curOldLine})
					curOldLine++
				case ' ':
					curHunk.Lines = append(curHunk.Lines, DiffLine{Type: ' ', Content: content, OldLine: curOldLine, NewLine: curNewLine})
					curOldLine++
					curNewLine++
				}
			}
		}
	}

	if curFile != nil {
		if curHunk != nil {
			curFile.Hunks = append(curFile.Hunks, *curHunk)
		}
		files = append(files, *curFile)
	}

	return files
}

func unquoteGitPath(p string) string {
	if strings.HasPrefix(p, "\"") && strings.HasSuffix(p, "\"") && len(p) >= 2 {
		unq, err := strconv.Unquote(p)
		if err == nil {
			return unq
		}
	}
	return p
}

// isNonSymbolConfigFile checks if a file is build/package configuration or manifest.
func isNonSymbolConfigFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if base == "go.mod" || base == "go.sum" || base == "package.json" ||
		base == "package-lock.json" || base == "cargo.toml" || base == "cargo.lock" ||
		base == "pyproject.toml" || base == "setup.cfg" || base == "requirements.txt" ||
		base == "tsconfig.json" || base == "jsconfig.json" {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml" || ext == ".toml" || ext == ".json"
}

// ExtractSymbolsFromSnapshotDiff parses a snapshot-aware git diff and extracts AST-enclosing symbols.
func ExtractSymbolsFromSnapshotDiff(ctx stdctx.Context, workDir string, diffResult *GitDiffResult) ([]Symbol, *CoverageReport, error) {
	cov := &CoverageReport{}
	if diffResult == nil || len(diffResult.Files) == 0 {
		cov.NoChanges = true
		return nil, cov, nil
	}

	var symbols []Symbol
	seenSymbols := make(map[string]bool)

	for _, file := range diffResult.Files {
		targetPath := file.NewPath
		if targetPath == "" {
			targetPath = file.OldPath
		}

		if file.IsBinary {
			cov.UnsupportedSyntax = append(cov.UnsupportedSyntax, fmt.Sprintf("binary file %s", targetPath))
			continue
		}

		// Check configuration and manifest files
		if isNonSymbolConfigFile(targetPath) {
			sym := Symbol{
				Name:      filepath.Base(targetPath),
				Kind:      "config",
				FilePath:  targetPath,
				StartLine: 1,
				Language:  "config",
				Workspace: workDir,
			}
			key := fmt.Sprintf("%s:%s", sym.FilePath, sym.Name)
			if !seenSymbols[key] {
				seenSymbols[key] = true
				symbols = append(symbols, sym)
			}
			continue
		}

		// Handle pure deleted file
		if file.IsDeleted && len(file.OldSource) > 0 {
			decls, err := extractDeclarationsFromAST(file.OldPath, file.OldSource)
			if err != nil {
				cov.UnsupportedSyntax = append(cov.UnsupportedSyntax, fmt.Sprintf("ast parse error %s: %v", file.OldPath, err))
			} else {
				for _, d := range decls {
					d.Kind = "deleted"
					d.SourceSnapshot = "old"
					d.Workspace = workDir
					key := fmt.Sprintf("%s:%s:%d", d.FilePath, d.Name, d.StartLine)
					if !seenSymbols[key] {
						seenSymbols[key] = true
						symbols = append(symbols, d)
					}
				}
			}
			continue
		}

		// Process hunks for modified or added files
		if len(file.NewSource) == 0 && len(file.OldSource) == 0 {
			continue
		}

		// Parse AST for new source (and old source if deletions exist)
		newDecls, newErr := extractDeclarationsFromAST(file.NewPath, file.NewSource)
		oldDecls, oldErr := extractDeclarationsFromAST(file.OldPath, file.OldSource)

		if newErr != nil && len(file.NewSource) > 0 {
			cov.UnsupportedSyntax = append(cov.UnsupportedSyntax, fmt.Sprintf("ast parse error %s: %v", file.NewPath, newErr))
			cov.IncompleteDiscovery = true
			// Conservative fallback: extract PascalCase or function-like names from hunk
			for _, h := range file.Hunks {
				for _, l := range h.Lines {
					if l.Type == '+' || l.Type == '-' {
						for _, m := range regexp.MustCompile(`\b[a-zA-Z_][a-zA-Z0-9_]+\b`).FindAllString(l.Content, -1) {
							if len(m) > 2 {
								key := fmt.Sprintf("%s:%s", file.NewPath, m)
								if !seenSymbols[key] {
									seenSymbols[key] = true
									symbols = append(symbols, Symbol{
										Name:      m,
										Kind:      "fallback",
										FilePath:  file.NewPath,
										Workspace: workDir,
									})
								}
							}
						}
					}
				}
			}
			continue
		}

		// Map hunk line edits to enclosing AST declarations
		for _, hunk := range file.Hunks {
			// Check if hunk is comment-only
			if isCommentOnlyHunk(hunk) {
				continue
			}

			// Check additions against new source declarations
			for _, addedLine := range hunk.AddedLines {
				decl := findEnclosingDecl(newDecls, addedLine)
				if decl != nil {
					declCopy := *decl
					declCopy.FilePath = file.NewPath
					declCopy.Workspace = workDir
					declCopy.SourceSnapshot = "new"
					key := fmt.Sprintf("%s:%s:%d", declCopy.FilePath, declCopy.Name, declCopy.StartLine)
					if !seenSymbols[key] {
						seenSymbols[key] = true
						symbols = append(symbols, declCopy)
					}
				}
			}

			// Check deletions against old source declarations
			if oldErr == nil && len(oldDecls) > 0 {
				for _, delLine := range hunk.DeletedLines {
					decl := findEnclosingDecl(oldDecls, delLine)
					if decl != nil {
						// Check if this declaration still exists in newDecls
						existsInNew := false
						for _, nd := range newDecls {
							if nd.Name == decl.Name && nd.Kind == decl.Kind {
								existsInNew = true
								break
							}
						}

						if !existsInNew {
							declCopy := *decl
							declCopy.FilePath = file.OldPath
							declCopy.Kind = "deleted"
							declCopy.Workspace = workDir
							declCopy.SourceSnapshot = "old"
							key := fmt.Sprintf("%s:%s:%d", declCopy.FilePath, declCopy.Name, declCopy.StartLine)
							if !seenSymbols[key] {
								seenSymbols[key] = true
								symbols = append(symbols, declCopy)
							}
						}
					}
				}
			}
		}
	}

	return symbols, cov, nil
}

// isCommentOnlyHunk checks if all changed lines in a hunk are single-line comments.
func isCommentOnlyHunk(hunk DiffHunk) bool {
	changedCount := 0
	for _, l := range hunk.Lines {
		if l.Type == '+' || l.Type == '-' {
			changedCount++
			trimmed := strings.TrimSpace(l.Content)
			if trimmed == "" {
				continue
			}
			if !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "/*") &&
				!strings.HasPrefix(trimmed, "*") && !strings.HasPrefix(trimmed, "#") {
				return false
			}
		}
	}
	return changedCount > 0
}

func findEnclosingDecl(decls []Symbol, line int) *Symbol {
	var best *Symbol
	for i := range decls {
		d := &decls[i]
		if line >= d.StartLine && line <= d.EndLine {
			if best == nil || (d.EndLine-d.StartLine) < (best.EndLine-best.StartLine) {
				best = d
			}
		}
	}
	return best
}

// extractDeclarationsFromAST uses Tree-sitter to parse declarations in a file.
func extractDeclarationsFromAST(filePath string, source []byte) ([]Symbol, error) {
	if len(source) == 0 {
		return nil, nil
	}

	entry := grammars.DetectLanguage(filepath.Base(filePath))
	if entry == nil || entry.Language() == nil {
		return nil, fmt.Errorf("no grammar for %s", filePath)
	}

	parser := gotreesitter.NewParser(entry.Language())
	tree, err := parser.Parse(source)
	if err != nil {
		return nil, err
	}
	defer tree.Release()

	bt := gotreesitter.Bind(tree)
	root := bt.RootNode()
	if root == nil {
		return nil, fmt.Errorf("empty root node")
	}

	langName := strings.ToLower(entry.Name)
	var decls []Symbol

	var walk func(node *gotreesitter.Node)
	walk = func(node *gotreesitter.Node) {
		if node == nil {
			return
		}

		nodeType := bt.NodeType(node)
		var nameNode *gotreesitter.Node
		kind := ""

		switch langName {
		case "go":
			if nodeType == "function_declaration" || nodeType == "method_declaration" {
				nameNode = bt.ChildByField(node, "name")
				kind = "function"
				if nodeType == "method_declaration" {
					kind = "method"
				}
			} else if nodeType == "type_spec" {
				nameNode = bt.ChildByField(node, "name")
				kind = "type"
			} else if nodeType == "var_spec" || nodeType == "const_spec" {
				nameNode = bt.ChildByField(node, "name")
				kind = "variable"
			}
		case "python":
			if nodeType == "function_definition" {
				nameNode = bt.ChildByField(node, "name")
				kind = "function"
			} else if nodeType == "class_definition" {
				nameNode = bt.ChildByField(node, "name")
				kind = "class"
			}
		case "typescript", "tsx", "javascript":
			if nodeType == "function_declaration" || nodeType == "method_definition" {
				nameNode = bt.ChildByField(node, "name")
				kind = "function"
				if nodeType == "method_definition" {
					kind = "method"
				}
			} else if nodeType == "class_declaration" || nodeType == "interface_declaration" || nodeType == "type_alias_declaration" {
				nameNode = bt.ChildByField(node, "name")
				kind = "type"
			} else if nodeType == "variable_declarator" {
				// Detect const foo = () => ... or function expression
				val := bt.ChildByField(node, "value")
				if val != nil {
					vt := bt.NodeType(val)
					if vt == "arrow_function" || vt == "function" {
						nameNode = bt.ChildByField(node, "name")
						kind = "function"
					}
				}
			}
		case "rust":
			if nodeType == "function_item" {
				nameNode = bt.ChildByField(node, "name")
				kind = "function"
			} else if nodeType == "struct_item" || nodeType == "enum_item" || nodeType == "trait_item" {
				nameNode = bt.ChildByField(node, "name")
				kind = "type"
			}
		}

		if nameNode != nil && kind != "" {
			name := string(source[nameNode.StartByte():nameNode.EndByte()])
			if name != "" {
				startLine := int(node.StartPoint().Row) + 1
				endLine := int(node.EndPoint().Row) + 1
				decls = append(decls, Symbol{
					Name:      name,
					Kind:      kind,
					FilePath:  filePath,
					StartLine: startLine,
					EndLine:   endLine,
					Language:  langName,
				})
			}
		}

		for i := 0; i < node.ChildCount(); i++ {
			walk(node.Child(i))
		}
	}

	walk(root)
	return decls, nil
}
