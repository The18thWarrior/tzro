package store

import (
	"reflect"
	"testing"
)

func TestAmbiguousColumnsPreserveExistingData(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.ImportTabular("example", []string{"ab", "value"}, [][]string{{"keep", "42"}}); err != nil {
		t.Fatal(err)
	}
	before, _, err := s.QuerySQL("SELECT * FROM example")
	if err != nil {
		t.Fatal(err)
	}
	for _, columns := range [][]string{{"a-b", "ab"}, {"A", "a"}, {"", "col_0"}} {
		if err = s.ImportTabular("example", columns, [][]string{{"replace", "99"}}); err == nil {
			t.Fatalf("accepted ambiguous schema %v", columns)
		}
		after, _, err := s.QuerySQL("SELECT * FROM example")
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("failed import changed data: %v %v", after, err)
		}
	}
}
