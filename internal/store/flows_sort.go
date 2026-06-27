package store

import (
	"strconv"
	"strings"
)

// FlowFilter sort/pagination fields (SortKey, SortDir, CursorID, CursorVal, BeforeID)
// are documented on FlowFilter in rules.go.

func normalizedFlowSort(f FlowFilter) (key string, dir int) {
	key = NormalizeFlowSortKey(f.SortKey)
	dir = f.SortDir
	if dir == 0 {
		if key == "id" || key == "time" {
			dir = -1
		} else {
			dir = 1
		}
	}
	if dir > 0 {
		dir = 1
	} else {
		dir = -1
	}
	return key, dir
}

// NormalizeFlowSortKey returns a whitelisted sort key (default id).
func NormalizeFlowSortKey(key string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "method", "host", "path", "status", "size", "time", "mime":
		return strings.ToLower(strings.TrimSpace(key))
	default:
		return "id"
	}
}

// flowSortExpr is the SQL expression used in ORDER BY and keyset cursors.
func flowSortExpr(key string) (expr string, ok bool) {
	switch NormalizeFlowSortKey(key) {
	case "method":
		return "method", true
	case "host":
		return "lower(host)", true
	case "path":
		return "lower(path)", true
	case "status":
		return "status", true
	case "size":
		return "COALESCE(res_len, 0)", true
	case "time":
		return "ts", true
	case "mime":
		return "lower(COALESCE(mime, ''))", true
	default:
		return "id", true
	}
}

func flowListOrderBy(f FlowFilter) string {
	key, dir := normalizedFlowSort(f)
	expr, _ := flowSortExpr(key)
	order := "DESC"
	idOrder := "DESC"
	if dir > 0 {
		order = "ASC"
		idOrder = "ASC"
	}
	return " ORDER BY " + expr + " " + order + ", id " + idOrder
}

func flowPageCursorID(f FlowFilter) int64 {
	if f.CursorID > 0 {
		return f.CursorID
	}
	return f.BeforeID
}

func bindSortCursorVal(key, curVal string) any {
	switch NormalizeFlowSortKey(key) {
	case "status", "size", "time", "id":
		n, _ := strconv.ParseInt(curVal, 10, 64)
		return n
	default:
		return curVal
	}
}

// appendFlowPageCursor adds a keyset WHERE clause for the next infinite-scroll page.
func appendFlowPageCursor(f FlowFilter, where []string, args []any) ([]string, []any) {
	cursorID := flowPageCursorID(f)
	if cursorID == 0 {
		return where, args
	}
	key, dir := normalizedFlowSort(f)
	if key == "id" {
		if dir < 0 {
			where = append(where, "id < ?")
		} else {
			where = append(where, "id > ?")
		}
		args = append(args, cursorID)
		return where, args
	}
	expr, _ := flowSortExpr(key)
	cur := bindSortCursorVal(key, f.CursorVal)
	if dir < 0 {
		where = append(where, "("+expr+" < ? OR ("+expr+" = ? AND id < ?))")
	} else {
		where = append(where, "("+expr+" > ? OR ("+expr+" = ? AND id > ?))")
	}
	args = append(args, cur, cur, cursorID)
	return where, args
}

// FlowSortValue returns the string form of a flow's sort key (for client cursors).
func FlowSortValue(fl *Flow, key string) string {
	switch NormalizeFlowSortKey(key) {
	case "method":
		return fl.Method
	case "host":
		return strings.ToLower(fl.Host)
	case "path":
		return strings.ToLower(fl.Path)
	case "status":
		return strconv.Itoa(fl.Status)
	case "size":
		return strconv.FormatInt(fl.ResLen, 10)
	case "time":
		return strconv.FormatInt(fl.TS.UnixMilli(), 10)
	case "mime":
		return strings.ToLower(fl.Mime)
	default:
		return strconv.FormatInt(fl.ID, 10)
	}
}
