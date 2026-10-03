package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tzro/pkg/store"
)

func TestProbeRecoveryMatchesSource(t *testing.T) {
	source := "package p\n// TOP_LEVEL\nfunc First() string {\n return \"" + strings.Repeat("unrelated_first_body ", 12) + "\"\n}\n\nfunc Second() string {\n return \"ZXQ_TARGET_PAYLOAD\"\n}\n"
	python := "def first():\n    return \"" + strings.Repeat("unrelated_first_body ", 12) + "\"\n\ndef second():\n    return \"PY_TARGET_PAYLOAD\"\n"
	cases := []struct {
		name, file, source, query, symbol, body string
		start, end, match                       int
		noStore, noHandle                       bool
	}{
		{"later_go", "functions.go", source, "ZXQ_TARGET_PAYLOAD", "Second", "ZXQ_TARGET_PAYLOAD", 7, 9, 8, false, false},
		{"named_go", "functions.go", source, "Second", "Second", "ZXQ_TARGET_PAYLOAD", 7, 9, 7, false, false},
		{"top_level", "functions.go", source, "TOP_LEVEL", "", "", 2, 2, 2, false, true},
		{"later_python", "functions.py", python, "PY_TARGET_PAYLOAD", "second", "PY_TARGET_PAYLOAD", 4, 5, 5, false, false},
		{"no_store", "functions.go", source, "ZXQ_TARGET_PAYLOAD", "Second", "", 7, 9, 8, true, true},
		{"unsupported_text", "notes.txt", "TEXT_PAYLOAD\n", "TEXT_PAYLOAD", "", "", 1, 1, 1, false, true},
		{"redacted", "functions.go", "package p\nfunc Sensitive()string{return \"needle sk-proj-1234567890abcdef1234567890abcdef\"}\n", "needle", "", "", 2, 2, 2, false, true},
	}
	rows := []map[string]any{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			var db *store.Store
			if !tc.noStore {
				var err error
				db, err = store.OpenStore(":memory:")
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
			}
			report, err := Probe(dir, tc.query, 10, db)
			if err != nil || len(report.Matches) != 1 {
				t.Fatalf("missing match: %v", err)
			}
			m := report.Matches[0]
			raw, _ := json.Marshal(m)
			fields := map[string]any{}
			_ = json.Unmarshal(raw, &fields)
			bodyOK := tc.noHandle && m.Hash == ""
			if !tc.noHandle && m.Hash != "" {
				body, e := db.GetBlob(m.Hash)
				bodyOK = e == nil && strings.Contains(body.Body, tc.body) && !strings.Contains(body.Body, "unrelated_first_body")
			}
			row := map[string]any{"case": tc.name, "symbol": m.SymbolName, "start": m.StartLine, "end": m.EndLine, "match_line": fields["match_line"], "body_correct": bodyOK, "pass": bodyOK && m.SymbolName == tc.symbol && m.StartLine == tc.start && m.EndLine == tc.end && fields["match_line"] == float64(tc.match)}
			rows = append(rows, row)
			if row["pass"] != true {
				t.Errorf("recovery: %+v", row)
			}
		})
	}
	t.Run("edited_source", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "functions.go")
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		db, err := store.OpenStore(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		first, err := Probe(dir, "ZXQ_TARGET_PAYLOAD", 10, db)
		if err != nil {
			t.Fatal(err)
		}
		changed := strings.Replace(source, "ZXQ_TARGET_PAYLOAD", "ZXQ_TARGET_PAYLOAD_LATEST", 1)
		if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
			t.Fatal(err)
		}
		second, err := Probe(dir, "ZXQ_TARGET_PAYLOAD", 10, db)
		if err != nil {
			t.Fatal(err)
		}
		oldHash, newHash := first.Matches[0].Hash, second.Matches[0].Hash
		good := oldHash != "" && newHash != "" && newHash != oldHash
		if good {
			body, e := db.GetBlob(newHash)
			good = e == nil && strings.Contains(body.Body, "LATEST")
		}
		row := map[string]any{"case": "edited_source", "fresh_body_and_hash": good, "pass": good}
		rows = append(rows, row)
		if !good {
			t.Error("probe did not recover edited matching body")
		}
	})
	if path := os.Getenv("TZRO_RECOVERY_MATRIX"); path != "" {
		b, _ := json.MarshalIndent(rows, "", "  ")
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
