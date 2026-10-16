package collrun

import (
	"strings"
	"testing"
)

func TestParseCSV(t *testing.T) {
	ds, err := ParseCSV(strings.NewReader("\ufeffuser,pass\nalice@example.com,a1\nbob@example.com,b2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if ds.Len() != 2 || ds.Rows[1]["user"] != "bob@example.com" || ds.Rows[0]["pass"] != "a1" {
		t.Fatalf("rows: %+v", ds.Rows)
	}
	if len(ds.Hash) != 64 || ds.Format != "csv" {
		t.Fatalf("hash/format: %q %q", ds.Hash, ds.Format)
	}
}

func TestParseCSVRaggedAndEmpty(t *testing.T) {
	ds, err := ParseCSV(strings.NewReader("a,b\n1\n2,3,4\n"))
	if err != nil {
		t.Fatal(err)
	}
	if ds.Rows[0]["b"] != "" || ds.Rows[1]["b"] != "3" {
		t.Fatalf("ragged rows: %+v", ds.Rows)
	}
	if _, err := ParseCSV(strings.NewReader("")); err == nil {
		t.Fatal("empty csv must fail")
	}
	if _, err := ParseCSV(strings.NewReader("a,a\n1,2\n")); err == nil {
		t.Fatal("duplicate header must fail")
	}
}

func TestParseJSON(t *testing.T) {
	ds, err := ParseJSON(strings.NewReader(`[{"id":7,"ok":true,"n":null,"o":{"a":1},"s":"x"}]`))
	if err != nil {
		t.Fatal(err)
	}
	r := ds.Rows[0]
	if r["id"] != "7" || r["ok"] != "true" || r["n"] != "" || r["o"] != `{"a":1}` || r["s"] != "x" {
		t.Fatalf("row: %+v", r)
	}
	if _, err := ParseJSON(strings.NewReader(`{"a":1}`)); err == nil {
		t.Fatal("non-array must fail")
	}
	if _, err := ParseJSON(strings.NewReader(`[1,2]`)); err == nil {
		t.Fatal("non-object rows must fail")
	}
}

func TestParseDataAutoAndLimit(t *testing.T) {
	if _, err := ParseData("x.json", strings.NewReader(`[{"a":"1"}]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseData("x.csv", strings.NewReader("a\n1\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseData("x.txt", strings.NewReader("a\n1\n")); err != nil {
		t.Fatalf("unknown extension sniffs csv: %v", err)
	}
	var sb strings.Builder
	sb.WriteString("a\n")
	for i := 0; i < MaxDataRows+1; i++ {
		sb.WriteString("1\n")
	}
	if _, err := ParseCSV(strings.NewReader(sb.String())); err == nil {
		t.Fatal("over MaxDataRows must fail")
	}
}

func TestDatasetRowWraps(t *testing.T) {
	ds := &Dataset{Rows: []map[string]string{{"a": "1"}, {"a": "2"}}}
	if ds.Row(0)["a"] != "1" || ds.Row(1)["a"] != "2" || ds.Row(2)["a"] != "1" {
		t.Fatal("row wrap")
	}
	var nilDS *Dataset
	if nilDS.Row(0) != nil || nilDS.Len() != 0 {
		t.Fatal("nil dataset")
	}
}
