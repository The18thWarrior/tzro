package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"tzro/pkg/dlp"
)

type discoveryPolicy struct {
	root     string
	engine   *dlp.PolicyEngine
	redactor *dlp.Redactor
}

func newDiscoveryPolicy(workspace string) (*discoveryPolicy, error) {
	policy, err := dlp.LoadWorkspacePolicy(workspace)
	if err != nil {
		return nil, fmt.Errorf("probe privacy policy: %w", err)
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	return &discoveryPolicy{root: root, engine: dlp.NewPolicyEngine(policy), redactor: dlp.NewRedactor()}, nil
}

func (p *discoveryPolicy) readableFile(path, relative string) (string, bool) {
	if !p.engine.EvaluatePath(filepath.ToSlash(relative)).Allowed {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(p.root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if !p.engine.EvaluatePath(filepath.ToSlash(rel)).Allowed || !p.engine.EvaluatePath(resolved).Allowed {
		return "", false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 2*1024*1024 {
		return "", false
	}
	return resolved, true
}
