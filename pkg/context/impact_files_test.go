package context

import (
	stdctx "context"
	"os"
	"path/filepath"
	"testing"
	"tzro/pkg/dlp"
)

func TestExplicitImpactFilesAndCallers(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"chosen.go": "package fixture\nfunc Chosen() int {return 1}\n", "unrelated.go": "package fixture\nfunc Unrelated() int {return 2}\n", "caller.go": "package fixture\nfunc Caller() int {return Chosen()}\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ia := NewImpactAnalyzer(nil, nil)
	r, pack, err := ia.AnalyzeFiles(stdctx.Background(), dir, []string{"chosen.go", "./chosen.go"}, 2000, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ChangedSymbols) != 1 || r.ChangedSymbols[0].Name != "Chosen" {
		t.Fatal("wrong anchors", r.ChangedSymbols)
	}
	found := false
	for _, edge := range r.ReferenceEdges {
		if edge.FilePath == "caller.go" {
			found = true
		}
	}
	if !found || pack == nil {
		t.Fatal("external caller missing", r.ReferenceEdges)
	}
	r, _, err = ia.AnalyzeFiles(stdctx.Background(), dir, []string{"chosen.go", "unrelated.go"}, 2000, false)
	if err != nil || len(r.ChangedSymbols) != 2 {
		t.Fatal("multiple file scope", r, err)
	}
}
func TestExplicitImpactFileBoundaries(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	for name, body := range map[string]string{"blocked.go": "package p\nfunc Blocked(){}", "ignored.go": "package p\nfunc Ignored(){}", "generated.pb.go": "package p\nfunc Generated(){}", "plain.txt": "not code", ".gitignore": "ignored.go\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "outside.go"), []byte("package p\nfunc Outside(){}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "outside.go"), filepath.Join(dir, "escape.go")); err != nil {
		t.Fatal(err)
	}
	policy := dlp.NewPolicyEngine(&dlp.WorkspacePolicy{DefaultAction: dlp.ActionAllow, Rules: []dlp.PolicyRule{{PathPattern: "blocked.go", Action: dlp.ActionDeny}}})
	ia := NewImpactAnalyzer(nil, policy)
	for _, file := range []string{"blocked.go", "ignored.go", "generated.pb.go", "plain.txt", "missing.go", "escape.go", "."} {
		if _, _, err := ia.AnalyzeFiles(stdctx.Background(), dir, []string{file}, 1000, false); err == nil {
			t.Fatalf("accepted %s", file)
		}
	}
	if _, _, err := ia.AnalyzeFiles(stdctx.Background(), dir, []string{"generated.pb.go"}, 1000, true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := stdctx.WithCancel(stdctx.Background())
	cancel()
	if _, _, err := ia.AnalyzeFiles(ctx, dir, []string{"generated.pb.go"}, 1000, true); err == nil {
		t.Fatal("ignored cancellation")
	}
}
