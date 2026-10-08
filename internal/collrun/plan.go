package collrun

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/collection"
	"github.com/Veyal/interseptor/internal/store"
)

// PlanItems lists the requests a run executes, in tree (rank) order. A
// non-empty pick selects exactly those requests, still in tree order (order =
// rank, so a picked set never runs in click order).
func PlanItems(items []store.Item, folderUID string, pick []string) []store.Item {
	var all []store.Item
	var walk func(ns []*collection.Node)
	walk = func(ns []*collection.Node) {
		for _, n := range ns {
			if n.Kind == "request" {
				all = append(all, n.Item)
			}
			walk(n.Children)
		}
	}
	roots := collection.BuildTree(items)
	if folderUID == "" {
		walk(roots)
	} else if f := findNode(roots, folderUID); f != nil && f.Kind == "folder" {
		walk(f.Children)
	}
	if len(pick) == 0 {
		return all
	}
	want := make(map[string]bool, len(pick))
	for _, u := range pick {
		want[u] = true
	}
	var out []store.Item
	for _, it := range all {
		if want[it.UID] {
			out = append(out, it)
		}
	}
	return out
}

func findNode(ns []*collection.Node, uid string) *collection.Node {
	for _, n := range ns {
		if n.UID == uid {
			return n
		}
		if f := findNode(n.Children, uid); f != nil {
			return f
		}
	}
	return nil
}

// FolderPath is the "A / B" folder path of an item.
func FolderPath(items []store.Item, itemUID string) string {
	by := make(map[string]store.Item, len(items))
	for _, it := range items {
		by[it.UID] = it
	}
	var parts []string
	seen := map[string]bool{itemUID: true}
	for p := by[itemUID].ParentUID; p != ""; {
		f, ok := by[p]
		if !ok || seen[p] {
			break
		}
		seen[p] = true
		parts = append([]string{f.Name}, parts...)
		p = f.ParentUID
	}
	return strings.Join(parts, " / ")
}

// FailedItemUIDs returns the items of a stored run that had a problem (error,
// block or non-passing test), for "rerun failed".
func FailedItemUIDs(rs RunStore, runUID string) ([]string, error) {
	rows, err := rs.ListRunResults(runUID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, row := range rows {
		var it ItemResult
		if row.ResultJSON == "" || json.Unmarshal([]byte(row.ResultJSON), &it) != nil {
			continue
		}
		if it.Problem() && !seen[row.ItemUID] {
			seen[row.ItemUID] = true
			out = append(out, row.ItemUID)
		}
	}
	sort.Strings(out)
	return out, nil
}

// nextIndex resolves a setNextRequest target (item name or uid) inside the
// plan. The first item with a matching name wins.
func nextIndex(plan []store.Item, target string) (int, bool) {
	for i, it := range plan {
		if it.UID == target {
			return i, true
		}
	}
	for i, it := range plan {
		if it.Name == target {
			return i, true
		}
	}
	return 0, false
}
