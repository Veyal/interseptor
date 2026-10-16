package collrun

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Dataset limits. Data files are uploaded content, never server paths.
const (
	MaxDataRows  = 10000
	MaxDataBytes = 32 << 20
)

// Dataset is the iteration data of a run: one row per iteration. Hash is the
// sha256 of the raw upload, stored with the run so a rerun can prove it used
// the same data.
type Dataset struct {
	Format  string              `json:"format"` // csv | json
	Columns []string            `json:"columns"`
	Rows    []map[string]string `json:"-"`
	Hash    string              `json:"hash"`
}

// Len is the number of rows (0 for nil).
func (d *Dataset) Len() int {
	if d == nil {
		return 0
	}
	return len(d.Rows)
}

// Row returns the data for iteration i; iterations beyond the data wrap
// around (Newman semantics).
func (d *Dataset) Row(i int) map[string]string {
	if d.Len() == 0 {
		return nil
	}
	return d.Rows[i%len(d.Rows)]
}

// Preview returns the first n rows.
func (d *Dataset) Preview(n int) []map[string]string {
	if d.Len() == 0 {
		return nil
	}
	if n > len(d.Rows) {
		n = len(d.Rows)
	}
	return d.Rows[:n]
}

func readLimited(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxDataBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxDataBytes {
		return nil, fmt.Errorf("data file exceeds %d bytes", MaxDataBytes)
	}
	return b, nil
}

func hashOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ParseData picks the format from the file name (.json / .csv) and falls back
// to sniffing a leading '[' for JSON, else CSV.
func ParseData(name string, r io.Reader) (*Dataset, error) {
	b, err := readLimited(r)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".json"):
		return parseJSONBytes(b)
	case strings.HasSuffix(lower, ".csv"):
		return parseCSVBytes(b)
	}
	if t := bytes.TrimLeft(b, "\ufeff \t\r\n"); len(t) > 0 && t[0] == '[' {
		return parseJSONBytes(b)
	}
	return parseCSVBytes(b)
}

// ParseCSV reads a CSV with a header row.
func ParseCSV(r io.Reader) (*Dataset, error) {
	b, err := readLimited(r)
	if err != nil {
		return nil, err
	}
	return parseCSVBytes(b)
}

func parseCSVBytes(b []byte) (*Dataset, error) {
	body := bytes.TrimPrefix(b, []byte("\ufeff"))
	cr := csv.NewReader(bytes.NewReader(body))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	head, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, errors.New("csv data file is empty")
	}
	if err != nil {
		return nil, fmt.Errorf("csv: %w", err)
	}
	seen := map[string]bool{}
	for i := range head {
		head[i] = strings.TrimSpace(head[i])
		if head[i] == "" {
			return nil, fmt.Errorf("csv: empty column name at position %d", i+1)
		}
		if seen[head[i]] {
			return nil, fmt.Errorf("csv: duplicate column %q", head[i])
		}
		seen[head[i]] = true
	}
	ds := &Dataset{Format: "csv", Columns: head, Hash: hashOf(b)}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("csv: %w", err)
		}
		if len(ds.Rows) >= MaxDataRows {
			return nil, fmt.Errorf("data file exceeds %d rows", MaxDataRows)
		}
		row := make(map[string]string, len(head))
		for i, col := range head {
			if i < len(rec) {
				row[col] = rec[i]
			} else {
				row[col] = ""
			}
		}
		ds.Rows = append(ds.Rows, row)
	}
	return ds, nil
}

// ParseJSON reads an array of flat-ish objects. Non-string values become their
// JSON text (null becomes empty), so {{col}} always resolves to a string.
func ParseJSON(r io.Reader) (*Dataset, error) {
	b, err := readLimited(r)
	if err != nil {
		return nil, err
	}
	return parseJSONBytes(b)
}

func parseJSONBytes(b []byte) (*Dataset, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(b, []byte("\ufeff"))))
	dec.UseNumber()
	var arr []map[string]any
	if err := dec.Decode(&arr); err != nil {
		return nil, fmt.Errorf("json data file must be an array of objects: %w", err)
	}
	if len(arr) > MaxDataRows {
		return nil, fmt.Errorf("data file exceeds %d rows", MaxDataRows)
	}
	ds := &Dataset{Format: "json", Hash: hashOf(b)}
	cols := map[string]bool{}
	for _, obj := range arr {
		row := make(map[string]string, len(obj))
		for k, v := range obj {
			row[k] = dataString(v)
			if !cols[k] {
				cols[k] = true
				ds.Columns = append(ds.Columns, k)
			}
		}
		ds.Rows = append(ds.Rows, row)
	}
	sortStrings(ds.Columns)
	return ds, nil
}

func dataString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}
