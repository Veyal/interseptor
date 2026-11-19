package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAbilitiesMCPToolsNeverExposeHumanOnlyControls(t *testing.T) {
	srv := New("http://127.0.0.1:1")
	registered := map[string]bool{}
	for _, name := range srv.ToolNames() {
		registered[name] = true
		lower := strings.ToLower(name)
		if strings.Contains(lower, "ability") || strings.Contains(lower, "abilities") {
			if name != "list_abilities" && name != "run_ability_action" {
				t.Errorf("unexpected ability tool registered: %q (trust/enable/install/remove must remain human-only)", name)
			}
		}
	}

	for _, required := range []string{"list_abilities", "run_ability_action"} {
		if !registered[required] {
			t.Fatalf("missing required ability tool %q", required)
		}
	}

	// Explicitly verify no human-only lifecycle tools exist.
	forbidden := []string{
		"trust_ability", "revoke_ability", "revoke_ability_trust",
		"enable_ability", "disable_ability", "toggle_ability",
		"install_ability", "scaffold_ability", "reload_abilities",
		"remove_ability", "delete_ability", "export_ability",
	}
	for _, name := range forbidden {
		if registered[name] {
			t.Errorf("forbidden human-only tool %q is registered", name)
		}
		if _, err := srv.Call(name, map[string]any{}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
			t.Errorf("Call(%q) = %v, want unknown tool", name, err)
		}
	}

	listDesc, _, ok := srv.ToolMeta("list_abilities")
	if !ok || !strings.Contains(strings.ToLower(listDesc), "human-only") {
		t.Errorf("list_abilities description must state human-only lifecycle: %q", listDesc)
	}
	runDesc, _, ok := srv.ToolMeta("run_ability_action")
	if !ok || !strings.Contains(runDesc, "trusted") || !strings.Contains(runDesc, "enabled") {
		t.Errorf("run_ability_action description must note trusted/enabled requirement: %q", runDesc)
	}

	webTools := ProfileTools(ProfileWebPentest)
	for _, want := range []string{"list_abilities", "run_ability_action"} {
		if !slices.Contains(webTools, want) {
			t.Errorf("ProfileWebPentest missing %q", want)
		}
	}
}

func TestListAbilitiesAndFiltering(t *testing.T) {
	const sampleJSON = `{
		"abilities": [
			{
				"id": "jwt-xray",
				"scope": "global",
				"trusted": true,
				"enabled": true,
				"quarantined": false,
				"drift": false,
				"manifest": {
					"schema": "interseptor.ability/v1",
					"id": "jwt-xray",
					"name": "JWT X-Ray",
					"version": "1.0.0",
					"capabilities": ["ui.inspectorTab", "ability.action", "mcp.tool"],
					"menus": [{"id": "panel", "label": "JWT X-Ray", "group": "abilities", "entry": "ui/panel.js"}],
					"contextMenus": [{"id": "ctx", "label": "Inspect JWT", "target": "flow", "action": "inspect"}],
					"inspectorTabs": [{"id": "tab", "label": "JWT", "side": "both", "entry": "ui/tab.js"}],
					"actions": [{"id": "inspect", "name": "Inspect", "entry": "hooks/inspect.star", "mcp": true}],
					"hooks": [{"event": "flow.captured", "entry": "hooks/on_flow.star"}]
				}
			},
			{
				"id": "paused-helper",
				"scope": "project",
				"trusted": true,
				"enabled": false,
				"quarantined": false,
				"manifest": {"schema": "interseptor.ability/v1", "id": "paused-helper", "name": "Paused", "version": "0.1.0"}
			},
			{
				"id": "untrusted-draft",
				"scope": "project",
				"trusted": false,
				"enabled": false,
				"quarantined": true,
				"manifest": {"schema": "interseptor.ability/v1", "id": "untrusted-draft", "name": "Draft", "version": "0.1.0"}
			}
		]
	}`

	var statusCode atomic.Int32
	statusCode.Store(http.StatusOK)
	var gotMethod, gotPath, gotSource string

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotSource = r.Header.Get("X-Interseptor-Source")
		if code := int(statusCode.Load()); code != http.StatusOK {
			http.Error(w, `{"error":"backend error"}`, code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, sampleJSON)
	}))
	defer mock.Close()

	srv := New(mock.URL)
	srv.report = func(Activity) {}

	decodeIDs := func(t *testing.T, raw string) []string {
		t.Helper()
		var out struct {
			Abilities []struct {
				ID string `json:"id"`
			} `json:"abilities"`
		}
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatalf("invalid JSON from list_abilities: %v\nraw=%s", err, raw)
		}
		if out.Abilities == nil {
			t.Fatalf("abilities array must not be null in JSON: %s", raw)
		}
		ids := make([]string, len(out.Abilities))
		for i, a := range out.Abilities {
			ids[i] = a.ID
		}
		return ids
	}

	// Unfiltered returns all abilities with manifest details intact.
	out, err := srv.Call("list_abilities", map[string]any{})
	if err != nil {
		t.Fatalf("list_abilities unfiltered: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/abilities" || gotSource != "ai" {
		t.Fatalf("unexpected request: %s %s X-Interseptor-Source=%q", gotMethod, gotPath, gotSource)
	}
	if ids := decodeIDs(t, out); !slices.Equal(ids, []string{"jwt-xray", "paused-helper", "untrusted-draft"}) {
		t.Fatalf("unfiltered ids = %v", ids)
	}
	if !strings.Contains(out, `"inspectorTabs"`) || !strings.Contains(out, `"contextMenus"`) {
		t.Fatalf("unfiltered response dropped manifest fields: %s", out)
	}

	// trustedOnly=true returns trusted abilities (enabled or disabled).
	out, err = srv.Call("list_abilities", map[string]any{"trustedOnly": true})
	if err != nil {
		t.Fatalf("list_abilities trustedOnly: %v", err)
	}
	if ids := decodeIDs(t, out); !slices.Equal(ids, []string{"jwt-xray", "paused-helper"}) {
		t.Fatalf("trustedOnly ids = %v", ids)
	}

	// enabledOnly=true returns only enabled abilities.
	out, err = srv.Call("list_abilities", map[string]any{"enabledOnly": true})
	if err != nil {
		t.Fatalf("list_abilities enabledOnly: %v", err)
	}
	if ids := decodeIDs(t, out); !slices.Equal(ids, []string{"jwt-xray"}) {
		t.Fatalf("enabledOnly ids = %v", ids)
	}

	// Both filters combined via JSON-RPC loose boolean string.
	text, isErr := callResult(t, srv, "list_abilities", `{"trustedOnly":"true","enabledOnly":true}`)
	if isErr {
		t.Fatalf("list_abilities JSON-RPC failed: %s", text)
	}
	if ids := decodeIDs(t, text); !slices.Equal(ids, []string{"jwt-xray"}) {
		t.Fatalf("trustedOnly+enabledOnly ids = %v", ids)
	}

	// Error status from control plane propagates.
	statusCode.Store(http.StatusInternalServerError)
	if _, err := srv.Call("list_abilities", map[string]any{}); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 error, got %v", err)
	}
}

func TestListAbilitiesEmptyFilteredReturnsEmptyArray(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"abilities":[{"id":"q","trusted":false,"enabled":false}]}`)
	}))
	defer mock.Close()

	srv := New(mock.URL)
	srv.report = func(Activity) {}

	out, err := srv.Call("list_abilities", map[string]any{"enabledOnly": true})
	if err != nil {
		t.Fatalf("list_abilities: %v", err)
	}
	if strings.TrimSpace(out) != `{"abilities":[]}` {
		t.Fatalf("filtered empty output = %q, want {\"abilities\":[]}", out)
	}
}

func TestRunAbilityAction(t *testing.T) {
	type captured struct {
		method      string
		escapedPath string
		contentType string
		source      string
		body        map[string]any
	}
	var last captured
	var hits atomic.Int32
	var respCode atomic.Int32
	var respBody atomic.Value
	respCode.Store(http.StatusOK)
	respBody.Store(`{"ok":true,"result":{"alg":"HS256","count":2}}`)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var decoded map[string]any
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		_ = dec.Decode(&decoded)
		last = captured{
			method:      r.Method,
			escapedPath: r.URL.EscapedPath(),
			contentType: r.Header.Get("Content-Type"),
			source:      r.Header.Get("X-Interseptor-Source"),
			body:        decoded,
		}
		w.WriteHeader(int(respCode.Load()))
		_, _ = io.WriteString(w, respBody.Load().(string))
	}))
	defer mock.Close()

	srv := NewProfile(mock.URL, ProfileWebPentest)
	srv.report = func(Activity) {}

	// 1. Merges input object and top-level flowId (>0), POSTs to escaped path.
	out, err := srv.Call("run_ability_action", map[string]any{
		"abilityId": "jwt-xray",
		"action":    "inspect",
		"flowId":    42,
		"input":     map[string]any{"strict": true, "flowId": 7, "limit": 5},
	})
	if err != nil {
		t.Fatalf("run_ability_action: %v", err)
	}
	if last.method != http.MethodPost || last.escapedPath != "/api/abilities/jwt-xray/actions/inspect" {
		t.Fatalf("request = %s %s", last.method, last.escapedPath)
	}
	if last.source != "ai" || last.contentType != "application/json" {
		t.Fatalf("headers source=%q contentType=%q", last.source, last.contentType)
	}
	if last.body["strict"] != true || fmtNumber(last.body["limit"]) != "5" || fmtNumber(last.body["flowId"]) != "42" {
		t.Fatalf("merged payload = %#v", last.body)
	}
	if !strings.Contains(out, `"HS256"`) {
		t.Fatalf("unexpected output: %s", out)
	}

	// 2. Preserves flowId from input when top-level flowId is omitted, and omits flowId when 0.
	if _, err := srv.Call("run_ability_action", map[string]any{
		"abilityId": "jwt-xray",
		"action":    "inspect",
		"flowId":    0,
		"input":     map[string]any{"flowId": 19, "mode": "fast"},
	}); err != nil {
		t.Fatalf("run_ability_action with input flowId: %v", err)
	}
	if fmtNumber(last.body["flowId"]) != "19" || last.body["mode"] != "fast" {
		t.Fatalf("payload when top-level flowId=0: %#v", last.body)
	}

	// 3. No input and no flowId sends empty JSON object.
	if _, err := srv.Call("run_ability_action", map[string]any{
		"abilityId": "jwt-xray",
		"action":    "ping",
	}); err != nil {
		t.Fatalf("run_ability_action empty payload: %v", err)
	}
	if len(last.body) != 0 {
		t.Fatalf("expected empty body, got %#v", last.body)
	}

	// 4. URL-escapes abilityId and action path segments.
	if _, err := srv.Call("run_ability_action", map[string]any{
		"abilityId": "my/ability id",
		"action":    "run action?x=1",
	}); err != nil {
		t.Fatalf("run_ability_action escaped path: %v", err)
	}
	if want := "/api/abilities/my%2Fability%20id/actions/run%20action%3Fx=1"; last.escapedPath != want {
		t.Fatalf("escapedPath = %q, want %q", last.escapedPath, want)
	}

	// 5. Propagates 403 error (untrusted / disabled / mcp:false) over Call and JSON-RPC.
	respCode.Store(http.StatusForbidden)
	respBody.Store(`{"error":"ability is not trusted: jwt-xray is not trusted and enabled"}`)
	if _, err := srv.Call("run_ability_action", map[string]any{
		"abilityId": "jwt-xray",
		"action":    "inspect",
	}); err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("expected 403 untrusted error, got %v", err)
	}
	text, isErr := callResult(t, srv, "run_ability_action", `{"abilityId":"jwt-xray","action":"inspect"}`)
	if !isErr || !strings.Contains(text, "403") || !strings.Contains(text, "not trusted") {
		t.Fatalf("JSON-RPC 403 error = %q isError=%v", text, isErr)
	}

	// 6. Rejects missing/invalid arguments before hitting control API.
	beforeHits := hits.Load()
	for _, tc := range []struct {
		args string
		want string
	}{
		{`{}`, "abilityId is required"},
		{`{"abilityId":"jwt-xray"}`, "action is required"},
		{`{"abilityId":"   ","action":"inspect"}`, "empty string"},
		{`{"abilityId":"jwt-xray","action":"   "}`, "empty string"},
		{`{"abilityId":"..","action":"inspect"}`, "invalid abilityId"},
		{`{"abilityId":"jwt-xray","action":"."}`, "invalid action"},
		{`{"abilityId":"jwt-xray","action":"inspect","flowId":"abc"}`, "flowId must be an integer"},
		{`{"abilityId":"jwt-xray","action":"inspect","input":"bad"}`, "input must be an object"},
	} {
		text, isErr := callResult(t, srv, "run_ability_action", tc.args)
		if !isErr || !strings.Contains(text, tc.want) {
			t.Errorf("args=%s -> %q isError=%v, want %q", tc.args, text, isErr, tc.want)
		}
	}
	if hits.Load() != beforeHits {
		t.Fatalf("invalid arguments reached control API (%d -> %d hits)", beforeHits, hits.Load())
	}
}

func fmtNumber(v any) string {
	if n, ok := v.(json.Number); ok {
		return n.String()
	}
	return ""
}
