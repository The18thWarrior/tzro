package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tzro/pkg/store"
)

func TestProbePolicyBoundaries(t *testing.T) {
	key := "sk-proj-1234567890abcdef1234567890abcdef"
	cases := []struct {
		name, file, policy, body, link string
		found, raw, indexed, wantError bool
	}{
		{name: "allowed", file: "source.go", body: "package p\nfunc Visible() string {return \"needle ordinary permitted source body\"}\n", found: true, indexed: true},
		{name: "default_env", file: ".env", body: "needle fixture"},
		{name: "default_secret_path", file: "secret.go", body: "package p\nfunc Visible() string {return \"needle\"}\n"},
		{name: "custom_path", file: "blocked.go", policy: `{"default_action":"allow","rules":[{"path_pattern":"blocked.go","action":"deny"}]}`, body: "package p\nfunc Visible() string {return \"needle\"}\n"},
		{name: "blocked_content", file: "source.go", policy: `{"default_action":"allow","rules":[{"data_class":"api_key","action":"block"}]}`, body: "package p\nfunc Visible() string {return \"needle " + key + "\"}\n"},
		{name: "redacted_content", file: "source.go", policy: `{"default_action":"redact"}`, body: "package p\nfunc Visible() string {return \"needle " + key + "\"}\n", found: true},
		{name: "automatic_redaction", file: "source.go", body: "package p\nfunc Visible() string {return \"needle " + key + "\"}\n", found: true},
		{name: "blocked_symlink", file: ".env", body: "needle fixture", link: "inside"},
		{name: "outside_symlink", file: "source.go", body: "package p\nfunc Visible() string {return \"needle\"}\n", link: "outside"},
		{name: "permitted_symlink", file: "source.go", body: "package p\nfunc Visible() string {return \"needle ordinary permitted source body\"}\n", link: "inside", found: true, indexed: true},
		{name: "malformed_policy", file: "source.go", policy: `{`, body: "package p\nfunc Visible() string {return \"needle\"}\n", wantError: true},
	}
	rows := []map[string]any{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			targetDir := dir
			if tc.link == "outside" {
				targetDir = t.TempDir()
			}
			target := filepath.Join(targetDir, tc.file)
			if err := os.WriteFile(target, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.link != "" {
				if err := os.Symlink(target, filepath.Join(dir, "alias.go")); err != nil {
					t.Fatal(err)
				}
			}
			if tc.policy != "" {
				if err := os.Mkdir(filepath.Join(dir, ".tzro"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".tzro/privacy.json"), []byte(tc.policy), 0600); err != nil {
					t.Fatal(err)
				}
			}
			db, err := store.OpenStore(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			report, err := Probe(dir, "needle", 20, db)
			found, raw, handles := false, false, false
			if report != nil {
				found = len(report.Matches) > 0
				raw = strings.Contains(report.FormatMarkdown(), key)
				for _, m := range report.Matches {
					handles = handles || m.Hash != ""
				}
			}
			syms, _ := db.SearchSymbols(dir, "Visible", 20)
			indexed := len(syms) > 0
			row := map[string]any{"case": tc.name, "found": found, "raw_sensitive_text": raw, "indexed": indexed, "has_recovery_handle": handles, "error": err != nil, "pass": found == tc.found && raw == tc.raw && indexed == tc.indexed && (err != nil) == tc.wantError}
			if (tc.name == "redacted_content" || tc.name == "automatic_redaction") && handles {
				row["pass"] = false
			}
			if tc.name == "allowed" && len(syms) > 0 {
				body, e := db.GetBlob(syms[0].Hash)
				if e != nil || !strings.Contains(body.Body, "ordinary permitted source body") {
					row["pass"] = false
				}
			}
			rows = append(rows, row)
			if row["pass"] != true {
				t.Errorf("policy result: %+v", row)
			}
		})
	}
	if path := os.Getenv("TZRO_POLICY_MATRIX"); path != "" {
		b, _ := json.MarshalIndent(rows, "", "  ")
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
