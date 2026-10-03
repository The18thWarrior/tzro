package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"tzro/pkg/store"
)

func TestIngestDistinctTailsRetainIndependentTables(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "store.db")
	t.Setenv("TZRO_DB_PATH", db)
	ingest := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		out, err := os.CreateTemp(dir, "output")
		if err != nil {
			t.Fatal(err)
		}
		original := os.Stdout
		os.Stdout = out
		cmd := newRootCmd()
		cmd.SetArgs([]string{"ingest", path})
		runErr := cmd.Execute()
		os.Stdout = original
		out.Close()
		if runErr != nil {
			t.Fatal(runErr)
		}
		b, err := os.ReadFile(out.Name())
		if err != nil {
			t.Fatal(err)
		}
		match := regexp.MustCompile("Table: `([^`]+)`").FindStringSubmatch(string(b))
		if len(match) != 2 {
			t.Fatalf("no table: %s", b)
		}
		return match[1]
	}
	a := ingest("a.csv", "key,amount\na,1\nb,2\nc,3\nd,4\n")
	b := ingest("b.csv", "key,amount\na,1\nb,2\nc,3\nd,9\n")
	if a == b {
		t.Fatal("distinct files alias the same table")
	}
	if again := ingest("a.csv", "key,amount\na,1\nb,2\nc,3\nd,4\n"); again != a {
		t.Fatal("identical input changed identity")
	}
	s, err := store.OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for table, want := range map[string]string{a: "10", b: "15"} {
		rows, _, err := s.QuerySQL("SELECT SUM(CAST(amount AS INTEGER)) AS total FROM " + table)
		if err != nil || len(rows) != 1 || rows[0]["total"] != want {
			t.Fatalf("%s: got %v, err %v; want %s", table, rows, err, want)
		}
	}
}
