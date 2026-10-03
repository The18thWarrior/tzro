package compactor

import (
	"strings"
	"testing"
	"tzro/pkg/store"
)

func TestEnvelopeAdvertisesQueryableColumns(t *testing.T) {
	for _, input := range []string{"net-total,user name\n10,Ada\n20,Lin\n", "net-total\tuser name\n10\tAda\n20\tLin\n", "金额,user name\n10,Ada\n20,Lin\n", ",user name\n10,Ada\n20,Lin\n"} {
		td, ok := DetectTabular(input)
		if !ok {
			t.Fatal("fixture detection")
		}
		original := append([]string{}, td.Columns...)
		s, err := store.OpenStore(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		if err = s.ImportTabular("example", td.Columns, td.Rows); err != nil {
			t.Fatal(err)
		}
		envelope := FormatEnvelope("example", td, 5)
		advertised := strings.Split(strings.Split(envelope, "Columns: ")[1], "\n")[0]
		columns := strings.Split(advertised, ", ")
		rows, _, err := s.QuerySQL(`SELECT SUM(CAST("` + columns[0] + `" AS INTEGER)) AS total FROM example`)
		s.Close()
		if err != nil || len(rows) != 1 || rows[0]["total"] != "30" {
			t.Fatalf("advertised columns %q: %v %v", advertised, rows, err)
		}
		if !strings.Contains(envelope, "Source column") {
			t.Fatal("missing source mapping")
		}
		for i := range original {
			if original[i] != td.Columns[i] {
				t.Fatal("source headers mutated")
			}
		}
	}
}
