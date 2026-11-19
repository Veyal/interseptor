package abilities

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"

	"github.com/Veyal/interseptor/internal/starx"
	"github.com/Veyal/interseptor/internal/store"
)

// FlowStore is the narrow persistence slice required by ability hooks and actions.
// *store.Store satisfies this interface.
type FlowStore interface {
	GetFlow(id int64) (*store.Flow, error)
	OpenBody(sum string) (io.ReadCloser, error)
	AddFlowTags(flowID int64, tags []string) ([]string, error)
	SetFlowNote(id int64, note string) error
}

const (
	maxStarlarkDepth = 128
	maxConvertNodes  = 50_000
	maxConvertBytes  = 2 << 20 // 2 MiB
)

var fileOptions = &syntax.FileOptions{Set: true}

var frozenStarxModule = func() *starlarkstruct.Module {
	m := &starlarkstruct.Module{
		Name:    "starx",
		Members: starx.Predeclared(),
	}
	m.Freeze()
	return m
}()

func predeclaredGlobals() starlark.StringDict {
	d := starx.Predeclared()
	d["starx"] = frozenStarxModule
	return d
}

func newStarThread(name string) *starlark.Thread {
	thread := &starlark.Thread{Name: name} // No Load callback: load() is disabled.
	thread.SetMaxExecutionSteps(MaxExecutionSteps)
	return thread
}

type compiledProgram struct {
	entry  string
	prog   *starlark.Program
	fnName string
}

type compiledFlowHook struct {
	abilityID string
	entry     string
	fn        starlark.Callable
}

type compiledAction struct {
	spec ActionSpec
	fn   starlark.Callable
}

// compileAbilityPrograms parses, resolves, and compiles Starlark scripts from
// an immutable snapshot WITHOUT executing any top-level module code.
func compileAbilityPrograms(snap abilitySnapshot, m AbilityManifest) ([]compiledProgram, map[string]compiledProgram, error) {
	if len(m.Hooks) == 0 && len(m.Actions) == 0 {
		return nil, nil, nil
	}

	var hookProgs []compiledProgram
	for _, hs := range m.Hooks {
		if hs.Event != EventFlowCaptured {
			continue
		}
		p, err := compileProgramFromSnapshot(snap, hs.Entry, "on_flow")
		if err != nil {
			return nil, nil, fmt.Errorf("hook %s: %w", hs.Entry, err)
		}
		hookProgs = append(hookProgs, p)
	}

	actionProgs := make(map[string]compiledProgram, len(m.Actions))
	for _, as := range m.Actions {
		p, err := compileProgramFromSnapshot(snap, as.Entry, "run")
		if err != nil {
			return nil, nil, fmt.Errorf("action %q (%s): %w", as.ID, as.Entry, err)
		}
		actionProgs[as.ID] = p
	}
	return hookProgs, actionProgs, nil
}

func compileProgramFromSnapshot(snap abilitySnapshot, relPath, fnName string) (cp compiledProgram, err error) {
	defer func() {
		if r := recover(); r != nil {
			cp = compiledProgram{}
			err = fmt.Errorf("starlark panic while compiling %s: %v", relPath, r)
		}
	}()

	src, ok := snap.files[relPath]
	if !ok {
		return compiledProgram{}, fmt.Errorf("referenced file %q is missing or hidden", relPath)
	}
	predeclared := predeclaredGlobals()
	file, prog, err := starlark.SourceProgramOptions(fileOptions, relPath, src, predeclared.Has)
	if err != nil {
		return compiledProgram{}, starx.ScriptError("compile "+relPath, err)
	}
	if prog.NumLoads() > 0 {
		return compiledProgram{}, fmt.Errorf("%s: load() is disabled in ability scripts", relPath)
	}
	if !hasTopLevelSymbol(file, fnName) {
		return compiledProgram{}, fmt.Errorf("%s: missing %s() function", relPath, fnName)
	}
	return compiledProgram{entry: relPath, prog: prog, fnName: fnName}, nil
}

func hasTopLevelSymbol(file *syntax.File, name string) bool {
	if file == nil {
		return false
	}
	for _, stmt := range file.Stmts {
		switch s := stmt.(type) {
		case *syntax.DefStmt:
			if s.Name != nil && s.Name.Name == name {
				return true
			}
		case *syntax.AssignStmt:
			if assignHasIdent(s.LHS, name) {
				return true
			}
		}
	}
	return false
}

func assignHasIdent(expr syntax.Expr, name string) bool {
	switch e := expr.(type) {
	case *syntax.Ident:
		return e.Name == name
	case *syntax.ParenExpr:
		return assignHasIdent(e.X, name)
	case *syntax.TupleExpr:
		for _, elem := range e.List {
			if assignHasIdent(elem, name) {
				return true
			}
		}
	case *syntax.ListExpr:
		for _, elem := range e.List {
			if assignHasIdent(elem, name) {
				return true
			}
		}
	}
	return false
}

// initAbilityPrograms initializes compiled Starlark programs. It must ONLY be
// called once an ability is trusted and enabled.
func initAbilityPrograms(m AbilityManifest, hookProgs []compiledProgram, actionProgs map[string]compiledProgram) ([]compiledFlowHook, map[string]compiledAction, error) {
	if len(hookProgs) == 0 && len(m.Actions) == 0 {
		return nil, nil, nil
	}

	var hooks []compiledFlowHook
	for _, p := range hookProgs {
		fn, err := initProgramCallable(p)
		if err != nil {
			return nil, nil, fmt.Errorf("hook %s: %w", p.entry, err)
		}
		hooks = append(hooks, compiledFlowHook{
			abilityID: m.ID,
			entry:     p.entry,
			fn:        fn,
		})
	}

	actions := make(map[string]compiledAction, len(m.Actions))
	for _, as := range m.Actions {
		p, ok := actionProgs[as.ID]
		if !ok {
			return nil, nil, fmt.Errorf("action %q (%s): compiled program missing", as.ID, as.Entry)
		}
		fn, err := initProgramCallable(p)
		if err != nil {
			return nil, nil, fmt.Errorf("action %q (%s): %w", as.ID, as.Entry, err)
		}
		actions[as.ID] = compiledAction{spec: as, fn: fn}
	}
	return hooks, actions, nil
}

func initProgramCallable(p compiledProgram) (fn starlark.Callable, err error) {
	defer func() {
		if r := recover(); r != nil {
			fn = nil
			err = fmt.Errorf("starlark panic while initializing %s: %v", p.entry, r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	thread := newStarThread("init:" + p.entry)
	stop := context.AfterFunc(ctx, func() { thread.Cancel("init timeout") })
	defer stop()

	globals, err := p.prog.Init(thread, predeclaredGlobals())
	if err != nil {
		return nil, starx.ScriptError("compile "+p.entry, err)
	}
	val, ok := globals[p.fnName]
	if !ok {
		return nil, fmt.Errorf("%s: missing %s() function", p.entry, p.fnName)
	}
	callable, ok := val.(starlark.Callable)
	if !ok {
		return nil, fmt.Errorf("%s: %s must be a function", p.entry, p.fnName)
	}
	globals.Freeze()
	return callable, nil
}

func flowToStarlark(st FlowStore, f *store.Flow) *starlark.Dict {
	d := starlark.NewDict(13)
	_ = d.SetKey(starlark.String("id"), starlark.MakeInt64(f.ID))
	_ = d.SetKey(starlark.String("method"), starlark.String(f.Method))
	_ = d.SetKey(starlark.String("scheme"), starlark.String(f.Scheme))
	_ = d.SetKey(starlark.String("host"), starlark.String(f.Host))
	_ = d.SetKey(starlark.String("port"), starlark.MakeInt(f.Port))
	_ = d.SetKey(starlark.String("path"), starlark.String(f.Path))
	_ = d.SetKey(starlark.String("status"), starlark.MakeInt(f.Status))
	_ = d.SetKey(starlark.String("mime"), starlark.String(f.Mime))
	_ = d.SetKey(starlark.String("reqHeaders"), headersToStarlark(f.ReqHeaders))
	_ = d.SetKey(starlark.String("resHeaders"), headersToStarlark(f.ResHeaders))
	_ = d.SetKey(starlark.String("reqBody"), starlark.String(readCappedBody(st, f.ReqBodyHash, MaxHookBodyBytes)))
	_ = d.SetKey(starlark.String("resBody"), starlark.String(readCappedBody(st, f.ResBodyHash, MaxHookBodyBytes)))
	_ = d.SetKey(starlark.String("note"), starlark.String(f.Note))
	d.Freeze()
	return d
}

func headersToStarlark(headers map[string][]string) *starlark.Dict {
	d := starlark.NewDict(len(headers))
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vals := headers[k]
		elems := make([]starlark.Value, len(vals))
		for i, v := range vals {
			elems[i] = starlark.String(v)
		}
		_ = d.SetKey(starlark.String(k), starlark.NewList(elems))
	}
	d.Freeze()
	return d
}

func readCappedBody(st FlowStore, sum string, limit int64) string {
	if st == nil || sum == "" || limit <= 0 {
		return ""
	}
	rc, err := st.OpenBody(sum)
	if err != nil || rc == nil {
		return ""
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit))
	if err != nil {
		return ""
	}
	return string(data)
}

func runFlowHook(ctx context.Context, h compiledFlowHook, flowVal *starlark.Dict) (tags []string, note string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	defer func() {
		if r := recover(); r != nil {
			tags = nil
			note = ""
			err = fmt.Errorf("hook %s/%s panicked: %v", h.abilityID, h.entry, r)
		}
	}()

	thread := newStarThread("hook:" + h.abilityID)
	stop := context.AfterFunc(ctx, func() { thread.Cancel("hook timeout") })
	defer stop()

	val, err := starlark.Call(thread, h.fn, starlark.Tuple{flowVal}, nil)
	if err != nil {
		return nil, "", starx.ScriptError("hook "+h.abilityID, err)
	}
	return parseHookResult(val)
}

func parseHookResult(val starlark.Value) ([]string, string, error) {
	if val == nil || val == starlark.None {
		return nil, "", nil
	}
	d, ok := val.(*starlark.Dict)
	if !ok {
		return nil, "", fmt.Errorf("on_flow must return None or dict, got %s", val.Type())
	}

	var tags []string
	if tv, ok, _ := d.Get(starlark.String("tags")); ok && tv != starlark.None {
		tags = extractBoundedTags(tv)
	}

	var note string
	if nv, ok, _ := d.Get(starlark.String("note")); ok && nv != starlark.None {
		if s, ok := starlark.AsString(nv); ok && strings.TrimSpace(s) != "" {
			note = truncateUTF8(s, MaxNoteLen)
		}
	}
	return tags, note, nil
}

func extractBoundedTags(v starlark.Value) []string {
	iter, ok := v.(starlark.Iterable)
	if !ok {
		return nil
	}
	// Do not iterate dicts as tag lists.
	if _, isDict := v.(*starlark.Dict); isDict {
		return nil
	}
	it := iter.Iterate()
	defer it.Done()

	seen := make(map[string]struct{}, MaxHookTags)
	var out []string
	var elem starlark.Value
	for it.Next(&elem) {
		s, ok := starlark.AsString(elem)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		s = truncateUTF8(s, MaxTagLen)
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
		if len(out) >= MaxHookTags {
			break
		}
	}
	return out
}

func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	b := s[:maxBytes]
	for len(b) > 0 && !utf8.ValidString(b) {
		b = b[:len(b)-1]
	}
	return b
}

func runActionScript(ctx context.Context, abilityID, actionID string, fn starlark.Callable, input map[string]any, st FlowStore) (res map[string]any, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	budget := &convertBudget{}
	ctxDict, err := mapToStarlarkDictBounded(input, 0, budget)
	if err != nil {
		return nil, fmt.Errorf("action input: %w", err)
	}
	// Also expose caller-supplied input under ctx["input"] when not explicitly provided,
	// matching the UI/Starlark SDK convention while keeping top-level input fields accessible.
	if _, hasInput := input["input"]; !hasInput {
		inDict, err := mapToStarlarkDictBounded(input, 0, budget)
		if err != nil {
			return nil, fmt.Errorf("action input: %w", err)
		}
		_ = ctxDict.SetKey(starlark.String("input"), inDict)
	}
	if flowID, ok := extractFlowID(input); ok && flowID > 0 && st != nil {
		f, err := st.GetFlow(flowID)
		if err != nil {
			return nil, fmt.Errorf("load flow %d: %w", flowID, err)
		}
		if f != nil {
			_ = ctxDict.SetKey(starlark.String("flow"), flowToStarlark(st, f))
		}
	}
	ctxDict.Freeze()

	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("action %s/%s panicked: %v", abilityID, actionID, r)
		}
	}()

	thread := newStarThread("action:" + abilityID + "/" + actionID)
	stop := context.AfterFunc(ctx, func() { thread.Cancel("action cancelled") })
	defer stop()

	val, err := starlark.Call(thread, fn, starlark.Tuple{ctxDict}, nil)
	if err != nil {
		return nil, starx.ScriptError("action "+abilityID+"/"+actionID, err)
	}
	goVal, err := starlarkToGo(val, 0)
	if err != nil {
		return nil, fmt.Errorf("convert action result: %w", err)
	}
	return map[string]any{
		"ok":     true,
		"result": goVal,
	}, nil
}

func extractFlowID(input map[string]any) (int64, bool) {
	if input == nil {
		return 0, false
	}
	v, ok := input["flowId"]
	if !ok || v == nil {
		if nested, ok := input["input"].(map[string]any); ok {
			v, ok = nested["flowId"]
			if !ok || v == nil {
				return 0, false
			}
		} else {
			return 0, false
		}
	}
	switch n := v.(type) {
	case int:
		return int64(n), n > 0
	case int64:
		return n, n > 0
	case int32:
		return int64(n), n > 0
	case uint64:
		if n > math.MaxInt64 {
			return 0, false
		}
		return int64(n), n > 0
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n > float64(math.MaxInt64) {
			return 0, false
		}
		return int64(n), true
	case json.Number:
		id, err := n.Int64()
		return id, err == nil && id > 0
	case string:
		id, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return id, err == nil && id > 0
	default:
		return 0, false
	}
}

type convertBudget struct {
	nodes int
	bytes int
}

func (b *convertBudget) takeNode(extraBytes int) error {
	b.nodes++
	if b.nodes > maxConvertNodes {
		return fmt.Errorf("value exceeds %d nodes", maxConvertNodes)
	}
	if extraBytes > 0 {
		b.bytes += extraBytes
		if b.bytes > maxConvertBytes {
			return fmt.Errorf("value exceeds %d bytes", maxConvertBytes)
		}
	}
	return nil
}

func mapToStarlarkDictBounded(m map[string]any, depth int, b *convertBudget) (*starlark.Dict, error) {
	if depth > maxStarlarkDepth {
		return nil, fmt.Errorf("input nested deeper than %d levels", maxStarlarkDepth)
	}
	if err := b.takeNode(0); err != nil {
		return nil, err
	}
	d := starlark.NewDict(len(m))
	if len(m) == 0 {
		return d, nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := b.takeNode(len(k)); err != nil {
			return nil, err
		}
		sv, err := goToStarlarkBounded(m[k], depth+1, b)
		if err != nil {
			return nil, err
		}
		_ = d.SetKey(starlark.String(k), sv)
	}
	return d, nil
}

func goToStarlarkBounded(v any, depth int, b *convertBudget) (starlark.Value, error) {
	if depth > maxStarlarkDepth {
		return nil, fmt.Errorf("value nested deeper than %d levels", maxStarlarkDepth)
	}
	switch x := v.(type) {
	case nil:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		return starlark.None, nil
	case bool:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		return starlark.Bool(x), nil
	case string:
		if err := b.takeNode(len(x)); err != nil {
			return nil, err
		}
		return starlark.String(x), nil
	case int:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		return starlark.MakeInt(x), nil
	case int64:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		return starlark.MakeInt64(x), nil
	case int32:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		return starlark.MakeInt64(int64(x)), nil
	case uint64:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		return starlark.MakeUint64(x), nil
	case float64:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		return starlark.Float(x), nil
	case float32:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		return starlark.Float(float64(x)), nil
	case json.Number:
		if err := b.takeNode(len(x)); err != nil {
			return nil, err
		}
		if i, err := x.Int64(); err == nil {
			return starlark.MakeInt64(i), nil
		}
		if f, err := x.Float64(); err == nil {
			return starlark.Float(f), nil
		}
		return starlark.String(x.String()), nil
	case []string:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		elems := make([]starlark.Value, len(x))
		for i, s := range x {
			if err := b.takeNode(len(s)); err != nil {
				return nil, err
			}
			elems[i] = starlark.String(s)
		}
		return starlark.NewList(elems), nil
	case []any:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		elems := make([]starlark.Value, len(x))
		for i, e := range x {
			sv, err := goToStarlarkBounded(e, depth+1, b)
			if err != nil {
				return nil, err
			}
			elems[i] = sv
		}
		return starlark.NewList(elems), nil
	case map[string]string:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		d := starlark.NewDict(len(x))
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := b.takeNode(len(k) + len(x[k])); err != nil {
				return nil, err
			}
			_ = d.SetKey(starlark.String(k), starlark.String(x[k]))
		}
		return d, nil
	case map[string][]string:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		for k, vals := range x {
			if err := b.takeNode(len(k)); err != nil {
				return nil, err
			}
			for _, vs := range vals {
				if err := b.takeNode(len(vs)); err != nil {
					return nil, err
				}
			}
		}
		return headersToStarlark(x), nil
	case map[string]any:
		return mapToStarlarkDictBounded(x, depth, b)
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		elems := make([]starlark.Value, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			sv, err := goToStarlarkBounded(rv.Index(i).Interface(), depth+1, b)
			if err != nil {
				return nil, err
			}
			elems[i] = sv
		}
		return starlark.NewList(elems), nil
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("unsupported map key type %s", rv.Type().Key())
		}
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		d := starlark.NewDict(rv.Len())
		for _, mk := range rv.MapKeys() {
			ks := mk.String()
			if err := b.takeNode(len(ks)); err != nil {
				return nil, err
			}
			sv, err := goToStarlarkBounded(rv.MapIndex(mk).Interface(), depth+1, b)
			if err != nil {
				return nil, err
			}
			_ = d.SetKey(starlark.String(ks), sv)
		}
		return d, nil
	}
	return nil, fmt.Errorf("unsupported input type %T", v)
}

func starlarkToGo(v starlark.Value, depth int) (any, error) {
	return starlarkToGoBounded(v, depth, &convertBudget{})
}

func starlarkToGoBounded(v starlark.Value, depth int, b *convertBudget) (any, error) {
	if depth > maxStarlarkDepth {
		return nil, fmt.Errorf("value nested deeper than %d levels", maxStarlarkDepth)
	}
	switch x := v.(type) {
	case nil, starlark.NoneType:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		return nil, nil
	case starlark.Bool:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		return bool(x), nil
	case starlark.String:
		s := string(x)
		if err := b.takeNode(len(s)); err != nil {
			return nil, err
		}
		return s, nil
	case starlark.Bytes:
		s := string(x)
		if err := b.takeNode(len(s)); err != nil {
			return nil, err
		}
		return s, nil
	case starlark.Int:
		if i, ok := x.Int64(); ok {
			if err := b.takeNode(0); err != nil {
				return nil, err
			}
			return i, nil
		}
		s := x.String()
		if err := b.takeNode(len(s)); err != nil {
			return nil, err
		}
		return s, nil
	case starlark.Float:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("non-finite float %v is not JSON serializable", f)
		}
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		return f, nil
	case *starlark.List:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		out := make([]any, 0, x.Len())
		it := x.Iterate()
		defer it.Done()
		var elem starlark.Value
		for it.Next(&elem) {
			gv, err := starlarkToGoBounded(elem, depth+1, b)
			if err != nil {
				return nil, err
			}
			out = append(out, gv)
		}
		return out, nil
	case starlark.Tuple:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		out := make([]any, len(x))
		for i, elem := range x {
			gv, err := starlarkToGoBounded(elem, depth+1, b)
			if err != nil {
				return nil, err
			}
			out[i] = gv
		}
		return out, nil
	case *starlark.Set:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		out := make([]any, 0, x.Len())
		it := x.Iterate()
		defer it.Done()
		var elem starlark.Value
		for it.Next(&elem) {
			gv, err := starlarkToGoBounded(elem, depth+1, b)
			if err != nil {
				return nil, err
			}
			out = append(out, gv)
		}
		return out, nil
	case *starlark.Dict:
		if err := b.takeNode(0); err != nil {
			return nil, err
		}
		out := make(map[string]any, x.Len())
		for _, item := range x.Items() {
			ks, ok := starlark.AsString(item[0])
			if !ok {
				return nil, fmt.Errorf("dict keys must be strings, got %s", item[0].Type())
			}
			if err := b.takeNode(len(ks)); err != nil {
				return nil, err
			}
			gv, err := starlarkToGoBounded(item[1], depth+1, b)
			if err != nil {
				return nil, err
			}
			out[ks] = gv
		}
		return out, nil
	default:
		return nil, fmt.Errorf("cannot convert %s to JSON value", v.Type())
	}
}
