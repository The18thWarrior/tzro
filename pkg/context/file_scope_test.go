package context

import (
	"reflect"
	"sort"
	"testing"
)

func TestFileScopeUsesLexicalBounds(t *testing.T) {
	src := []byte("package p\nvar Global int; func F() {var local int; _=local}; type GlobalType int\nvar Closure = func(){var captured int; _=captured}\n")
	got, e := extractFileDeclarationsFromAST("a.go", src, true)
	if e != nil {
		t.Fatal(e)
	}
	names := []string{}
	for _, d := range got {
		names = append(names, d.Name)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"Closure", "F", "Global", "GlobalType"}) {
		t.Fatal(names)
	}
	original, e := extractDeclarationsFromAST("a.go", src)
	if e != nil {
		t.Fatal(e)
	}
	if len(original) <= len(got) {
		t.Fatal("diff extraction unexpectedly filtered")
	}
}
