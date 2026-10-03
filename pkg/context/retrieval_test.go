package context

import (
	"os"
	"path/filepath"
	"testing"

	"tzro/pkg/store"
	"tzro/pkg/tokenizer"
)

func TestAssembleCountsTheCompleteMarkdownEnvelope(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "guide.md"), []byte("Read the configuration before starting.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, withTrace := range []bool{false, true} {
		var db *store.Store
		if withTrace {
			db = s
		}
		pack, err := NewAssembler(db, nil).Assemble(workspace, "guide.md", 1000)
		if err != nil {
			t.Fatal(err)
		}
		if (pack.TraceID != "") != withTrace {
			t.Fatalf("unexpected trace state: %+v", pack)
		}
		want := tokenizer.CountDefault(pack.FormatMarkdown())
		if pack.Tokenizer == nil || pack.Tokenizer.SerializedPackTokens != want {
			t.Fatalf("withTrace=%v: reported %+v, rendered Markdown requires %d tokens", withTrace, pack.Tokenizer, want)
		}
	}
}

func TestAssembleKeepsStrongSymbolMatchAheadOfPathMatches(t *testing.T) {
	workspace := t.TempDir()
	files := map[string]string{
		"a_ReconcileInvoice_helpers.go": "package service\nfunc UnrelatedHelper() {}\n",
		"z_payments.go":                 "package service\nfunc ReconcileInvoice() {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pack, err := NewAssembler(s, nil).Assemble(workspace, "ReconcileInvoice", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Items) == 0 || pack.Items[0].SymbolName != "ReconcileInvoice" {
		t.Fatalf("strong declaration match should lead the pack, got %+v", pack.Items)
	}
}

func TestAssembleFindsFileNamedInSentence(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "architecture.md"), []byte("Component responsibilities and dependencies.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"Follow architecture.md.",
		"Follow `architecture.md`.",
		"Follow (architecture.md).",
		"Follow “architecture.md”.",
	} {
		t.Run(query, func(t *testing.T) {
			pack, err := NewAssembler(nil, nil).Assemble(workspace, query, 1000)
			if err != nil {
				t.Fatal(err)
			}
			if len(pack.Items) != 1 || pack.Items[0].FilePath != "architecture.md" {
				t.Fatalf("explicit document should be included, got %+v", pack.Items)
			}
		})
	}
}
