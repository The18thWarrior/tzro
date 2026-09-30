package probe

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tzro/pkg/store"
	"unicode/utf8"
)

func TestProbeNontextBoundary(t *testing.T) {
	key := "sk-proj-1234567890abcdef1234567890abcdef"
	cases := []struct {
		name, file, body, policy string
		found, sqlite, wantError bool
		skipped                  int
	}{
		{name: "nul_binary", file: "sample.bin", body: "prefix\x00needle payload", skipped: 1},
		{name: "invalid_utf8", file: "sample.data", body: "needle \xff\xfe payload", skipped: 1},
		{name: "binary_source_extension", file: "sample.go", body: "package p\nfunc Needle(){println(\"needle\x00\")}\n", skipped: 1},
		{name: "invalid_source_extension", file: "sample.py", body: "def target():\n return 'needle\xff'\n", skipped: 1},
		{name: "sqlite", file: "artifact.db", sqlite: true, skipped: 1},
		{name: "unicode_go", file: "source.go", body: "package p\nfunc Visible() string {return \"needle 雪 café\"}\n", found: true},
		{name: "unicode_plain", file: "notes.txt", body: "needle 雪 café 🙂\n", found: true},
		{name: "bom", file: "notes.txt", body: "\xef\xbb\xbfneedle ordinary UTF8 text\n", found: true},
		{name: "crlf", file: "source.go", body: "package p\r\nfunc Visible() string {return \"needle\"}\r\n", found: true},
		{name: "empty", file: "empty.txt"},
		{name: "no_match", file: "notes.txt", body: "ordinary valid text"},
		{name: "denied_path", file: "blocked.bin", body: "needle\x00", policy: `{"default_action":"allow","rules":[{"path_pattern":"blocked.bin","action":"deny"}]}`},
		{name: "denied_content", file: "blocked.bin", body: "needle\x00" + key, policy: `{"default_action":"allow","rules":[{"data_class":"api_key","action":"block"}]}`},
		{name: "malformed_policy", file: "source.go", body: "needle", policy: `{`, wantError: true},
	}
	rows := []map[string]any{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, tc.file)
			if fixture := os.Getenv("TZRO_NONTEXT_SQLITE_FIXTURE"); tc.sqlite && fixture != "" {
				b, err := os.ReadFile(fixture)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(file, b, 0600); err != nil {
					t.Fatal(err)
				}
			} else if tc.sqlite {
				db, e := store.OpenStore(file)
				if e != nil {
					t.Fatal(e)
				}
				_, e = db.PutArtifact(&store.Artifact{Type: "log", Body: "needle SQLite fixture"})
				if e != nil {
					t.Fatal(e)
				}
				if e = db.Close(); e != nil {
					t.Fatal(e)
				}
			} else if e := os.WriteFile(file, []byte(tc.body), 0600); e != nil {
				t.Fatal(e)
			}
			source, e := os.ReadFile(file)
			if e != nil {
				t.Fatal(e)
			}
			if tc.policy != "" {
				if e = os.Mkdir(filepath.Join(dir, ".tzro"), 0700); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(dir, ".tzro/privacy.json"), []byte(tc.policy), 0600); e != nil {
					t.Fatal(e)
				}
			}
			db, e := store.OpenStore(":memory:")
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			result, e := Probe(dir, "needle", 100, db)
			if tc.wantError {
				pass := e != nil
				rows = append(rows, map[string]any{"case": tc.name, "source_sha256": fmt.Sprintf("%x", sha256.Sum256(source)), "pass": pass, "error": e != nil})
				if !pass {
					t.Error("malformed policy did not fail")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			encoded, e := json.Marshal(result)
			if e != nil {
				t.Fatal(e)
			}
			var object map[string]any
			if e = json.Unmarshal(encoded, &object); e != nil {
				t.Fatal(e)
			}
			skipped, _ := object["skipped_nontext_files"].(float64)
			markdown := result.FormatMarkdown()
			valid := utf8.ValidString(markdown) && !strings.ContainsRune(markdown, 0)
			found := len(result.Matches) > 0
			recovered := true
			bodyHashes := []string{}
			for _, m := range result.Matches {
				if m.Hash != "" {
					blob, e := db.GetBlob(m.Hash)
					ok := e == nil && blob.Body != "" && bytes.Contains(source, []byte(blob.Body))
					recovered = recovered && ok
					if ok {
						bodyHashes = append(bodyHashes, fmt.Sprintf("%x", sha256.Sum256([]byte(blob.Body))))
					}
				}
			}
			after, e := os.ReadFile(file)
			if e != nil {
				t.Fatal(e)
			}
			unchanged := bytes.Equal(source, after)
			pass := found == tc.found && int(skipped) == tc.skipped && valid && recovered && unchanged
			rows = append(rows, map[string]any{"case": tc.name, "source_sha256": fmt.Sprintf("%x", sha256.Sum256(source)), "matches": len(result.Matches), "skipped_nontext": int(skipped), "valid_text_output": valid, "exact_recovery": recovered, "recovered_body_sha256": bodyHashes, "source_unchanged": unchanged, "pass": pass})
			if !pass {
				t.Errorf("found=%v want=%v skipped=%v want=%v valid=%v recovery=%v unchanged=%v", found, tc.found, skipped, tc.skipped, valid, recovered, unchanged)
			}
		})
	}
	if path := os.Getenv("TZRO_NONTEXT_MATRIX"); path != "" {
		b, e := json.MarshalIndent(rows, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
