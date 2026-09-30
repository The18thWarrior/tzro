package context

import (
	stdctx "context"
	"fmt"
	ignore "github.com/sabhiram/go-gitignore"
	"os"
	"path/filepath"
	"strings"
)

// AnalyzeFiles anchors impact in current declarations from explicit workspace
// files. Reference discovery still spans the workspace, independent of git diff.
func (ia *ImpactAnalyzer) AnalyzeFiles(ctx stdctx.Context, workspaceRoot string, files []string, budget int, includeGenerated bool) (*ImpactReport, *ContextPack, error) {
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("impact requires at least one file")
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, nil, err
	}
	var excludes *ignore.GitIgnore
	if data, err := os.ReadFile(filepath.Join(root, ".gitignore")); err == nil {
		excludes = ignore.CompileIgnoreLines(strings.Split(string(data), "\n")...)
	}
	seen := map[string]bool{}
	var symbols []Symbol
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		path := file
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		logical, err := filepath.Rel(root, path)
		if err != nil {
			return nil, nil, err
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, nil, fmt.Errorf("impact file %q: %w", file, err)
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil {
			return nil, nil, err
		}
		for _, name := range []string{logical, rel} {
			if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
				return nil, nil, fmt.Errorf("impact file %q is outside the workspace", file)
			}
			if ia.policy != nil && !ia.policy.EvaluatePath(filepath.ToSlash(name)).Allowed {
				return nil, nil, fmt.Errorf("impact file %q is blocked by policy", file)
			}
			if excludes != nil && excludes.MatchesPath(filepath.ToSlash(name)) {
				return nil, nil, fmt.Errorf("impact file %q is ignored", file)
			}
			if !includeGenerated && isGeneratedFile(name) {
				return nil, nil, fmt.Errorf("impact file %q is generated; use --include-generated", file)
			}
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("impact file %q is not a regular file", file)
		}
		source, err := os.ReadFile(resolved)
		if err != nil {
			return nil, nil, err
		}
		decls, err := extractFileDeclarationsFromAST(filepath.ToSlash(rel), source, true)
		if err != nil {
			return nil, nil, fmt.Errorf("impact file %q: %w", file, err)
		}
		if len(decls) == 0 {
			return nil, nil, fmt.Errorf("impact file %q has no supported declarations", file)
		}
		for i := range decls {
			decls[i].Workspace = root
			decls[i].SourceSnapshot = "working_tree"
		}
		symbols = append(symbols, decls...)
	}
	return ia.AnalyzeReport(ctx, root, symbols, budget, includeGenerated, "explicit_files")
}
