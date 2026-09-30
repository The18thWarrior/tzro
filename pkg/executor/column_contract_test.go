package executor

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"tzro/pkg/store"
)

func TestIngestMetadataMatchesQuerySchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(path, []byte("net-total,user name\n10,Ada\n20,Lin\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d := NewBuiltinDispatcher(dir, s)
	metadata, err := d.Dispatch(context.Background(), "ingest", map[string]any{"file": path, "table": "example"})
	if err != nil {
		t.Fatal(err)
	}
	_, columns, err := s.QuerySQL("SELECT * FROM example")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(metadata["columns"], columns) {
		t.Fatalf("metadata %v != schema %v", metadata, columns)
	}
	if !reflect.DeepEqual(metadata["source_columns"], []string{"net-total", "user name"}) {
		t.Fatalf("source mapping: %v", metadata)
	}
}
