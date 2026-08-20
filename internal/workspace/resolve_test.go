package workspace

import (
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestResolveFromRoots_SingleRoot(t *testing.T) {
	roots := []*mcp.Root{
		{URI: "file:///Users/jp/repos/project"},
	}
	rootPath, extras := ResolveFromRoots(roots)
	if rootPath != "/Users/jp/repos/project" {
		t.Errorf("rootPath = %q, want %q", rootPath, "/Users/jp/repos/project")
	}
	if len(extras) != 0 {
		t.Errorf("extras = %v, want empty", extras)
	}
}

func TestResolveFromRoots_MultipleRoots(t *testing.T) {
	roots := []*mcp.Root{
		{URI: "file:///a"},
		{URI: "file:///b"},
		{URI: "file:///c"},
	}
	rootPath, extras := ResolveFromRoots(roots)
	if rootPath != "/a" {
		t.Errorf("rootPath = %q, want %q", rootPath, "/a")
	}
	if len(extras) != 2 {
		t.Fatalf("extras length = %d, want 2", len(extras))
	}
	if extras[0] != "/b" || extras[1] != "/c" {
		t.Errorf("extras = %v, want [/b, /c]", extras)
	}
}

func TestResolveFromRoots_Empty(t *testing.T) {
	rootPath, extras := ResolveFromRoots(nil)
	if rootPath != "" {
		t.Errorf("rootPath = %q, want empty", rootPath)
	}
	if extras != nil {
		t.Errorf("extras = %v, want nil", extras)
	}
}

func TestResolveFromRoots_NonFileURI(t *testing.T) {
	roots := []*mcp.Root{
		{URI: "https://example.com"},
	}
	rootPath, extras := ResolveFromRoots(roots)
	if rootPath != "" {
		t.Errorf("rootPath = %q, want empty for non-file URI", rootPath)
	}
	if extras != nil {
		t.Errorf("extras = %v, want nil for non-file URI", extras)
	}
}

func TestResolveFromRoots_MixedURIs(t *testing.T) {
	roots := []*mcp.Root{
		{URI: "https://example.com"},
		{URI: "file:///real/path"},
	}
	rootPath, _ := ResolveFromRoots(roots)
	if rootPath != "/real/path" {
		t.Errorf("rootPath = %q, want %q — should skip non-file URIs", rootPath, "/real/path")
	}
}

func TestResolveFromEnv_Set(t *testing.T) {
	t.Setenv("TZRO_WORKSPACE", "/some/path")
	got := ResolveFromEnv()
	if got != "/some/path" {
		t.Errorf("ResolveFromEnv = %q, want %q", got, "/some/path")
	}
}

func TestResolveFromEnv_Unset(t *testing.T) {
	os.Unsetenv("TZRO_WORKSPACE")
	got := ResolveFromEnv()
	if got != "" {
		t.Errorf("ResolveFromEnv = %q, want empty", got)
	}
}
