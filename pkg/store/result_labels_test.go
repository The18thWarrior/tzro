package store

import (
	"reflect"
	"strings"
	"testing"
)

func TestQueryResultLabelIntegrity(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ImportTabular("example", []string{"value"}, [][]string{{"7"}}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"SELECT 1 AS value, 2 AS value FROM example",
		"SELECT 1 AS value, 1 AS value FROM example",
		"SELECT 1 AS value, 2 AS value FROM example WHERE 0",
		"SELECT a.value, b.value FROM example a JOIN example b ON 1",
		"SELECT NULL AS x, 2 AS x FROM example",
	} {
		t.Run(query, func(t *testing.T) {
			rows, _, err := s.QuerySQL(query)
			if err == nil || !strings.Contains(err.Error(), "distinct AS aliases") || rows != nil {
				t.Fatalf("ambiguous result accepted: rows=%v err=%v", rows, err)
			}
		})
	}
	for _, tc := range []struct {
		query string
		want  map[string]string
	}{
		{"SELECT 1 AS left_value, 2 AS right_value FROM example", map[string]string{"left_value": "1", "right_value": "2"}},
		{"SELECT 1 AS value, 2 AS Value FROM example", map[string]string{"value": "1", "Value": "2"}},
		{"SELECT NULL AS absent, value FROM example", map[string]string{"absent": "", "value": "7"}},
		{"SELECT a.value AS left_value, b.value AS right_value FROM example a JOIN example b ON 1", map[string]string{"left_value": "7", "right_value": "7"}},
	} {
		rows, cols, err := s.QuerySQL(tc.query)
		if err != nil || len(rows) != 1 || len(cols) != 2 || !reflect.DeepEqual(rows[0], tc.want) {
			t.Fatalf("valid query: %s got %v %v %v", tc.query, rows, cols, err)
		}
	}
	rows, cols, err := s.QuerySQL("SELECT value FROM example WHERE 0")
	if err != nil || len(rows) != 0 || !reflect.DeepEqual(cols, []string{"value"}) {
		t.Fatalf("empty query: %v %v %v", rows, cols, err)
	}
	rows, _, err = s.QuerySQL("SELECT value FROM example")
	if err != nil || len(rows) != 1 || rows[0]["value"] != "7" {
		t.Fatalf("stored data changed: %v %v", rows, err)
	}
}
