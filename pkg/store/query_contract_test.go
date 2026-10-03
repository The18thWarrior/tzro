package store

import (
	"reflect"
	"testing"
)

func TestQueryRejectsStackedMutationsBeforeExecution(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ImportTabular("example", []string{"value"}, [][]string{{"7"}, {"9"}}); err != nil {
		t.Fatal(err)
	}
	before, cols, err := s.QuerySQL("SELECT value FROM example ORDER BY value")
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"SELECT 1; DELETE FROM example", "SELECT 1; UPDATE example SET value='0'", "SELECT 1; DROP TABLE example", "SELECT 1; INSERT INTO example VALUES ('99')", "SELECT 1; PRAGMA query_only=OFF; DELETE FROM example",
		"SELECT 1; /* marker */ DELETE FROM example", "SELECT 1; -- marker\nDELETE FROM example", "SELECT 'abc';DELETE FROM example", "SELECT 1\x00; DELETE FROM example", "DELETE FROM example", "SELECT 'unterminated", "SELECT 1 /* unterminated",
	} {
		t.Run(sql, func(t *testing.T) {
			if rows, _, err := s.QuerySQL(sql); err == nil || rows != nil {
				t.Fatalf("query accepted: %v %v", rows, err)
			}
			after, afterCols, err := s.QuerySQL("SELECT value FROM example ORDER BY value")
			if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(cols, afterCols) {
				t.Fatalf("mutation occurred: %v %v", after, err)
			}
		})
	}
}
func TestQuerySingleSelectLexicalBoundaries(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cases := map[string]string{
		"SELECT '; DELETE FROM example' AS value":                   "; DELETE FROM example",
		"SELECT 'it''s; data' AS value;":                            "it's; data",
		"SELECT 7 AS value; -- trailing ; DELETE\n":                 "7",
		"SELECT /* ; DELETE */ 7 AS value; /* trailing ; DELETE */": "7",
		"/* leading */ SELECT 7 AS value":                           "7",
		"SELECT 7 AS [semi;name]":                                   "7",
		"SELECT 7 AS \"semi;name\"":                                 "7",
		"SELECT 7 AS `semi;name`":                                   "7",
		"SELECT 7 AS \"doubled\"\";name\"":                          "7",
	}
	for sql, want := range cases {
		rows, cols, err := s.QuerySQL(sql)
		if err != nil || len(rows) != 1 || len(cols) != 1 || rows[0][cols[0]] != want {
			t.Fatalf("valid SELECT failed: %q -> %v %v %v", sql, rows, cols, err)
		}
	}
}
