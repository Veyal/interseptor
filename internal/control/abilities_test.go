package control

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/abilities"
	"github.com/Veyal/interseptor/internal/store"
)

func abilityUIHeaders() map[string]string {
	return map[string]string{
		"X-Interseptor-CSRF": "1",
		"Cookie":             uiSessionCookie + "=" + uiSessionSecret,
	}
}

func makeAbilityTarGz(t *testing.T, id string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for rel, content := range files {
		name := id + "/" + rel
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeAbilityZip(t *testing.T, id string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for rel, content := range files {
		hdr := &zip.FileHeader{
			Name:   id + "/" + rel,
			Method: zip.Deflate,
		}
		hdr.SetMode(0o644)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sampleAbilityFiles(id string) map[string]string {
	manifest := `{
  "schema": "interseptor.ability/v1",
  "id": "` + id + `",
  "name": "Sample ` + id + `",
  "version": "1.0.0",
  "description": "Test ability for ` + id + `",
  "author": "Tester",
  "capabilities": ["ui.menu", "ui.panel", "ability.action", "mcp.tool"],
  "menus": [{"id": "main", "label": "Main", "group": "recon", "entry": "ui/panel.js", "style": "ui/style.css"}],
  "hooks": [{"event": "flow.captured", "entry": "hooks/on_flow.star"}],
  "actions": [
    {"id": "inspect", "name": "Inspect", "entry": "hooks/inspect.star", "mcp": true},
    {"id": "local-only", "name": "Local Only", "entry": "hooks/local.star", "mcp": false}
  ]
}`
	return map[string]string{
		"ability.json": manifest,
		"ui/panel.js":  "export function mount(root) { root.textContent = 'mounted'; }\n",
		"ui/style.css": ".panel { color: inherit; }\n",
		"hooks/on_flow.star": `def on_flow(flow):
    return None
`,
		"hooks/inspect.star": `def run(ctx):
    flow = ctx.get("flow")
    return {
        "ability": "` + id + `",
        "count": ctx.get("count", 0),
        "isInt": type(ctx.get("count", 0)) == "int",
        "host": flow["host"] if flow else "",
    }
`,
		"hooks/local.star": `def run(ctx):
    return {"local": True}
`,
	}
}

func doControlReq(t *testing.T, method, url string, body io.Reader, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("NewRequest %s %s: %v", method, url, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body %s %s: %v", method, url, err)
	}
	return resp.StatusCode, resp.Header, data
}

func TestAbilitiesDefaultHubWithoutDirsReturnsEmpty(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	code, _, body := doControlReq(t, http.MethodGet, ts.URL+"/api/abilities", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/abilities = %d, body=%s", code, body)
	}
	var out struct {
		Abilities []abilities.Ability `json:"abilities"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Abilities) != 0 {
		t.Fatalf("expected empty abilities when dirs are unconfigured, got %d", len(out.Abilities))
	}

	code, _, _ = doControlReq(t, http.MethodGet, ts.URL+"/api/abilities/missing", nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("GET /api/abilities/missing = %d, want 404", code)
	}

	code, _, _ = doControlReq(t, http.MethodGet, ts.URL+"/abilities/missing/sha256:0000/ui/panel.js", nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("GET /abilities/missing/sha256:0000/ui/panel.js = %d, want 404", code)
	}
}

func TestAbilitiesOpenAPICoverageAndCatalogParity(t *testing.T) {
	wants := []string{
		"GET /api/abilities",
		"GET /api/abilities/{id}",
		"POST /api/abilities/install",
		"POST /api/abilities/scaffold",
		"POST /api/abilities/reload",
		"POST /api/abilities/{id}/trust",
		"POST /api/abilities/{id}/revoke",
		"POST /api/abilities/{id}/toggle",
		"POST /api/abilities/{id}/actions/{action}",
		"GET /api/abilities/{id}/export",
		"DELETE /api/abilities/{id}",
	}
	indexed := make(map[string]bool, len(apiRoutes))
	for _, r := range apiRoutes {
		indexed[r.Method+" "+r.Path] = true
	}
	regBytes, err := os.ReadFile("routes_register.go")
	if err != nil {
		t.Fatal(err)
	}
	regSrc := string(regBytes)
	for _, want := range wants {
		if !indexed[want] {
			t.Errorf("apiRoutes missing %s", want)
		}
		if !strings.Contains(regSrc, `HandleFunc("`+want+`"`) {
			t.Errorf("routes_register.go missing %s", want)
		}
	}
	if !strings.Contains(regSrc, `HandleFunc("GET /abilities/{id}/{revision}/{asset...}"`) {
		t.Errorf("routes_register.go missing GET /abilities/{id}/{revision}/{asset...}")
	}

	doc := openapiDoc(t)
	paths := doc["paths"].(map[string]any)

	exportItem := paths["/api/abilities/{id}/export"].(map[string]any)["get"].(map[string]any)
	exportResp := exportItem["responses"].(map[string]any)["200"].(map[string]any)
	if c, _ := exportResp["content"].(map[string]any); c["application/json"] != nil {
		t.Fatalf("GET /api/abilities/{id}/export must not claim application/json response")
	}

	installItem := paths["/api/abilities/install"].(map[string]any)["post"].(map[string]any)
	installResp := installItem["responses"].(map[string]any)["200"].(map[string]any)
	if c, _ := installResp["content"].(map[string]any); c["application/json"] == nil {
		t.Fatalf("POST /api/abilities/install should declare application/json response")
	}
}

func TestAbilitiesUISessionEnforcement(t *testing.T) {
	h, st, _ := newHub(t)
	if err := h.ConfigureAbilities(t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	fullKey, _, err := st.CreateAPIKey("agent", store.ScopeFull, 0)
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// Create a real ability via interactive UI session first.
	code, _, body := doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/scaffold",
		strings.NewReader(`{"id":"gate-test","name":"Gate Test"}`),
		abilityUIHeaders(),
	)
	if code != http.StatusCreated {
		t.Fatalf("scaffold with UI session = %d, body=%s", code, body)
	}

	tgz := makeAbilityTarGz(t, "other-test", sampleAbilityFiles("other-test"))

	badCallers := map[string]map[string]string{
		"ai source with cookie and csrf": {
			"X-Interseptor-Source": "ai",
			"X-Interseptor-CSRF":   "1",
			"Cookie":               uiSessionCookie + "=" + uiSessionSecret,
		},
		"other source with cookie and csrf": {
			"X-Interseptor-Source": "cli",
			"X-Interseptor-CSRF":   "1",
			"Cookie":               uiSessionCookie + "=" + uiSessionSecret,
		},
		"bearer full key with cookie and csrf": {
			"Authorization":      "Bearer " + fullKey,
			"X-Interseptor-CSRF": "1",
			"Cookie":             uiSessionCookie + "=" + uiSessionSecret,
		},
		"missing csrf header": {
			"Cookie": uiSessionCookie + "=" + uiSessionSecret,
		},
		"missing ui cookie": {
			"X-Interseptor-CSRF": "1",
		},
		"forged ui cookie": {
			"X-Interseptor-CSRF": "1",
			"Cookie":             uiSessionCookie + "=forged-secret",
		},
	}

	routes := []struct {
		method string
		path   string
		body   func() io.Reader
	}{
		{http.MethodPost, "/api/abilities/install", func() io.Reader { return bytes.NewReader(tgz) }},
		{http.MethodPost, "/api/abilities/scaffold", func() io.Reader { return strings.NewReader(`{"id":"evil-tool","name":"Evil"}`) }},
		{http.MethodPost, "/api/abilities/reload", func() io.Reader { return nil }},
		{http.MethodPost, "/api/abilities/gate-test/trust", func() io.Reader { return strings.NewReader(`{"enabled":true}`) }},
		{http.MethodPost, "/api/abilities/gate-test/revoke", func() io.Reader { return nil }},
		{http.MethodPost, "/api/abilities/gate-test/toggle", func() io.Reader { return strings.NewReader(`{"enabled":true}`) }},
		{http.MethodDelete, "/api/abilities/gate-test", func() io.Reader { return nil }},
	}

	for callerName, hdrs := range badCallers {
		for _, rt := range routes {
			code, _, respBody := doControlReq(t, rt.method, ts.URL+rt.path, rt.body(), hdrs)
			if code != http.StatusForbidden {
				t.Errorf("%s -> %s %s = %d (want 403): %s", callerName, rt.method, rt.path, code, respBody)
			}
		}
	}

	// Confirm gate-test remained quarantined and undeleted.
	code, _, body = doControlReq(t, http.MethodGet, ts.URL+"/api/abilities/gate-test", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("GET gate-test after rejected calls = %d, body=%s", code, body)
	}
	var got struct {
		Ability abilities.Ability `json:"ability"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Ability.Trusted || got.Ability.Enabled || !got.Ability.Quarantined {
		t.Fatalf("rejected calls mutated trust state: %+v", got.Ability)
	}
}

func TestAbilitiesQuarantineTrustAssetsActionExportDriftAndDelete(t *testing.T) {
	globalDir := t.TempDir()
	projectDir := t.TempDir()

	h, st, _ := newHub(t)
	h.GlobalDir = globalDir
	h.ProjectDir = projectDir
	if err := h.ConfigureAbilities(globalDir, projectDir); err != nil {
		t.Fatal(err)
	}

	flowID, err := st.InsertFlow(&store.Flow{
		TS:         time.Now(),
		Method:     "GET",
		Scheme:     "https",
		Host:       "api.example.com",
		Port:       443,
		Path:       "/v1/items",
		Status:     200,
		ResHeaders: map[string][]string{"Content-Type": {"application/json"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	fullKey, _, err := st.CreateAPIKey("mcp-agent", store.ScopeFull, 0)
	if err != nil {
		t.Fatal(err)
	}
	readKey, _, err := st.CreateAPIKey("viewer", store.ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// Subscribe to SSE to verify ability.update events.
	sseCtx, sseCancel := context.WithCancel(context.Background())
	defer sseCancel()
	sseReq, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, ts.URL+"/api/events", nil)
	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		t.Fatal(err)
	}
	defer sseResp.Body.Close()
	sseEvents := make(chan string, 32)
	go func() {
		sc := bufio.NewScanner(sseResp.Body)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"ability.update"`) {
				sseEvents <- line
			}
		}
	}()

	// 1. Raw .tar.gz install into default global scope.
	tgz := makeAbilityTarGz(t, "tar-tool", sampleAbilityFiles("tar-tool"))
	code, _, body := doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/install", bytes.NewReader(tgz), abilityUIHeaders())
	if code != http.StatusCreated {
		t.Fatalf("raw install = %d, body=%s", code, body)
	}
	var installResp struct {
		Ability abilities.Ability `json:"ability"`
	}
	if err := json.Unmarshal(body, &installResp); err != nil {
		t.Fatal(err)
	}
	if installResp.Ability.ID != "tar-tool" || installResp.Ability.Scope != abilities.ScopeGlobal ||
		installResp.Ability.Trusted || installResp.Ability.Enabled || !installResp.Ability.Quarantined {
		t.Fatalf("unexpected installed ability: %+v", installResp.Ability)
	}

	select {
	case <-sseEvents:
	case <-time.After(2 * time.Second):
		t.Fatal("expected ability.update SSE event after install")
	}

	// 2. Multipart .zip install into ?scope=project.
	zp := makeAbilityZip(t, "zip-tool", sampleAbilityFiles("zip-tool"))
	var mpBuf bytes.Buffer
	mw := multipart.NewWriter(&mpBuf)
	fw, err := mw.CreateFormFile("file", "zip-tool.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(zp); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	mpHdrs := abilityUIHeaders()
	mpHdrs["Content-Type"] = mw.FormDataContentType()
	code, _, body = doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/install?scope=project", &mpBuf, mpHdrs)
	if code != http.StatusCreated {
		t.Fatalf("multipart zip install = %d, body=%s", code, body)
	}
	var zipResp struct {
		Ability abilities.Ability `json:"ability"`
	}
	if err := json.Unmarshal(body, &zipResp); err != nil {
		t.Fatal(err)
	}
	if zipResp.Ability.ID != "zip-tool" || zipResp.Ability.Scope != abilities.ScopeProject || !zipResp.Ability.Quarantined {
		t.Fatalf("unexpected multipart installed ability: %+v", zipResp.Ability)
	}

	assetPath := func(id, digest, rel string) string {
		return "/abilities/" + url.PathEscape(id) + "/" + url.PathEscape(digest) + "/" + strings.TrimPrefix(rel, "/")
	}

	// 3. While quarantined: assets 404, toggle fails, action fails.
	code, _, _ = doControlReq(t, http.MethodGet, ts.URL+assetPath("tar-tool", installResp.Ability.Digest, "ui/panel.js"), nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("quarantined asset GET = %d, want 404", code)
	}
	code, _, _ = doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/tar-tool/toggle", strings.NewReader(`{"enabled":true}`), abilityUIHeaders())
	if code != http.StatusForbidden && code != http.StatusConflict {
		t.Fatalf("toggle untrusted = %d, want 403/409", code)
	}
	code, _, _ = doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/tar-tool/actions/inspect", strings.NewReader(`{}`), nil)
	if code != http.StatusForbidden && code != http.StatusConflict {
		t.Fatalf("action on untrusted = %d, want 403/409", code)
	}

	// 4. Approval is bound to the reviewed digest and scope.
	trustURL := ts.URL + "/api/abilities/tar-tool/trust"
	code, _, _ = doControlReq(t, http.MethodPost, trustURL, strings.NewReader(`{"enabled":false}`), abilityUIHeaders())
	if code != http.StatusBadRequest {
		t.Fatalf("approval without reviewed digest and scope = %d, want 400", code)
	}
	approval := func(digest, scope string, enabled bool) io.Reader {
		return bytes.NewReader(jsonMustMarshal(t, map[string]any{"digest": digest, "scope": scope, "enabled": enabled}))
	}
	code, _, body = doControlReq(t, http.MethodPost, trustURL, approval(installResp.Ability.Digest, abilities.ScopeProject, true), abilityUIHeaders())
	if code != http.StatusConflict {
		t.Fatalf("approval with stale scope = %d, want 409: %s", code, body)
	}
	code, _, body = doControlReq(t, http.MethodPost, trustURL, approval(installResp.Ability.Digest, abilities.ScopeGlobal, false), abilityUIHeaders())
	if code != http.StatusOK {
		t.Fatalf("trust(enabled=false) = %d, body=%s", code, body)
	}
	code, _, _ = doControlReq(t, http.MethodGet, ts.URL+assetPath("tar-tool", installResp.Ability.Digest, "ui/panel.js"), nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("trusted-but-disabled asset GET = %d, want 404", code)
	}

	// 5. Toggle enable -> asset served with expected headers and content.
	code, _, body = doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/tar-tool/toggle", strings.NewReader(`{"enabled":true}`), abilityUIHeaders())
	if code != http.StatusOK {
		t.Fatalf("toggle(true) = %d, body=%s", code, body)
	}
	code, hdr, assetBody := doControlReq(t, http.MethodGet, ts.URL+assetPath("tar-tool", installResp.Ability.Digest, "ui/panel.js"), nil, nil)
	if code != http.StatusOK {
		t.Fatalf("enabled asset GET = %d, body=%s", code, assetBody)
	}
	if ct := hdr.Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Fatalf("asset Content-Type = %q, want text/javascript; charset=utf-8", ct)
	}
	if cc := hdr.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("asset Cache-Control = %q, want no-store", cc)
	}
	if nosniff := hdr.Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Fatalf("asset X-Content-Type-Options = %q, want nosniff", nosniff)
	}
	if !strings.Contains(string(assetBody), "export function mount") {
		t.Fatalf("unexpected asset body: %s", assetBody)
	}
	// Wrong revision digest must 404.
	code, _, _ = doControlReq(t, http.MethodGet, ts.URL+assetPath("tar-tool", "sha256:0000", "ui/panel.js"), nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("wrong-revision asset GET = %d, want 404", code)
	}

	// Blocked paths never served.
	for _, blocked := range []string{
		assetPath("tar-tool", installResp.Ability.Digest, "ability.json"),
		assetPath("tar-tool", installResp.Ability.Digest, "hooks/inspect.star"),
		assetPath("tar-tool", installResp.Ability.Digest, "ui/missing.js"),
	} {
		code, _, _ = doControlReq(t, http.MethodGet, ts.URL+blocked, nil, nil)
		if code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", blocked, code)
		}
	}

	// 6. Execute action: interactive UI, authenticated full-scope AI/MCP, read-only rejected, non-MCP blocked for any non-UI caller.
	actionPayload := jsonMustMarshal(t, map[string]any{"flowId": flowID, "count": 7})
	code, _, body = doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/tar-tool/actions/inspect", bytes.NewReader(actionPayload), abilityUIHeaders())
	if code != http.StatusOK {
		t.Fatalf("UI action = %d, body=%s", code, body)
	}
	var actRes struct {
		OK     bool           `json:"ok"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(body, &actRes); err != nil {
		t.Fatal(err)
	}
	if !actRes.OK || actRes.Result["host"] != "api.example.com" || actRes.Result["isInt"] != true || actRes.Result["count"] != float64(7) {
		t.Fatalf("unexpected action result: %+v", actRes)
	}

	// Full-scope API key + AI source on mcp:true action succeeds.
	code, _, body = doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/tar-tool/actions/inspect", bytes.NewReader(actionPayload), map[string]string{
		"Authorization":        "Bearer " + fullKey,
		"X-Interseptor-Source": "ai",
	})
	if code != http.StatusOK {
		t.Fatalf("full-key AI action = %d, body=%s", code, body)
	}

	// Non-UI callers (with or without AI source) on mcp:false action are rejected (403); UI session succeeds.
	code, _, _ = doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/tar-tool/actions/local-only", strings.NewReader(`{}`), map[string]string{
		"Authorization":        "Bearer " + fullKey,
		"X-Interseptor-Source": "ai",
	})
	if code != http.StatusForbidden {
		t.Fatalf("AI caller on non-MCP action = %d, want 403", code)
	}
	code, _, _ = doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/tar-tool/actions/local-only", strings.NewReader(`{}`), map[string]string{
		"Authorization": "Bearer " + fullKey,
	})
	if code != http.StatusForbidden {
		t.Fatalf("full-key unmarked non-UI caller on local-only action = %d, want 403", code)
	}
	code, _, body = doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/tar-tool/actions/local-only", strings.NewReader(`{}`), abilityUIHeaders())
	if code != http.StatusOK {
		t.Fatalf("UI caller on local-only action = %d, want 200: %s", code, body)
	}

	// Read-only key cannot POST action.
	code, _, _ = doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/tar-tool/actions/inspect", strings.NewReader(`{}`), map[string]string{
		"Authorization": "Bearer " + readKey,
	})
	if code != http.StatusForbidden {
		t.Fatalf("read-only key action = %d, want 403", code)
	}

	// Unknown action returns 404.
	code, _, _ = doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/tar-tool/actions/does-not-exist", strings.NewReader(`{}`), abilityUIHeaders())
	if code != http.StatusNotFound {
		t.Fatalf("missing action = %d, want 404", code)
	}

	// 7. Export deterministic .tar.gz archive.
	code, hdr, exp1 := doControlReq(t, http.MethodGet, ts.URL+"/api/abilities/tar-tool/export", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("export = %d, body=%s", code, exp1)
	}
	if ct := hdr.Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("export Content-Type = %q, want application/gzip", ct)
	}
	if cd := hdr.Get("Content-Disposition"); cd != `attachment; filename="tar-tool.tar.gz"` {
		t.Fatalf("export Content-Disposition = %q", cd)
	}
	code, _, exp2 := doControlReq(t, http.MethodGet, ts.URL+"/api/abilities/tar-tool/export", nil, nil)
	if code != http.StatusOK || !bytes.Equal(exp1, exp2) {
		t.Fatalf("export not deterministic (code=%d)", code)
	}

	// 8. Tamper on disk BEFORE reload must NOT alter bytes served for approved revision;
	// after reload it detects drift, auto-disables, and blocks asset & action!
	tamperPath := filepath.Join(globalDir, "abilities", "tar-tool", "ui", "panel.js")
	if err := os.WriteFile(tamperPath, []byte("export function mount() { alert('tampered'); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, preReloadAsset := doControlReq(t, http.MethodGet, ts.URL+assetPath("tar-tool", installResp.Ability.Digest, "ui/panel.js"), nil, nil)
	if code != http.StatusOK || strings.Contains(string(preReloadAsset), "tampered") {
		t.Fatalf("asset before reload leaked tampered disk bytes (code=%d, body=%s)", code, preReloadAsset)
	}
	code, _, body = doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/reload", nil, abilityUIHeaders())
	if code != http.StatusOK {
		t.Fatalf("reload = %d, body=%s", code, body)
	}
	code, _, body = doControlReq(t, http.MethodGet, ts.URL+"/api/abilities/tar-tool", nil, nil)
	if code != http.StatusOK {
		t.Fatal(err)
	}
	var drifted struct {
		Ability abilities.Ability `json:"ability"`
	}
	_ = json.Unmarshal(body, &drifted)
	if !drifted.Ability.Drift || drifted.Ability.Trusted || drifted.Ability.Enabled || !drifted.Ability.Quarantined {
		t.Fatalf("expected drift auto-quarantine after reload, got %+v", drifted.Ability)
	}
	code, _, _ = doControlReq(t, http.MethodGet, ts.URL+assetPath("tar-tool", drifted.Ability.Digest, "ui/panel.js"), nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("drifted asset GET = %d, want 404", code)
	}
	code, _, _ = doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/tar-tool/toggle", strings.NewReader(`{"enabled":true}`), abilityUIHeaders())
	if code != http.StatusConflict {
		t.Fatalf("toggle drifted = %d, want 409", code)
	}
	code, _, _ = doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/tar-tool/actions/inspect", strings.NewReader(`{}`), abilityUIHeaders())
	if code != http.StatusConflict {
		t.Fatalf("action on drifted = %d, want 409", code)
	}

	// 9. Re-trust must pass current digest & scope (stale digest returns 409), then revoke, then delete.
	code, _, _ = doControlReq(t, http.MethodPost, trustURL, approval(installResp.Ability.Digest, abilities.ScopeGlobal, true), abilityUIHeaders())
	if code != http.StatusConflict {
		t.Fatalf("re-trust with stale digest = %d, want 409", code)
	}
	code, _, body = doControlReq(t, http.MethodPost, trustURL, approval(drifted.Ability.Digest, drifted.Ability.Scope, true), abilityUIHeaders())
	if code != http.StatusOK {
		t.Fatalf("re-trust = %d, body=%s", code, body)
	}
	code, _, _ = doControlReq(t, http.MethodGet, ts.URL+assetPath("tar-tool", drifted.Ability.Digest, "ui/panel.js"), nil, nil)
	if code != http.StatusOK {
		t.Fatalf("re-trusted asset GET = %d, want 200", code)
	}

	code, _, body = doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/tar-tool/revoke", nil, abilityUIHeaders())
	if code != http.StatusOK {
		t.Fatalf("revoke = %d, body=%s", code, body)
	}
	code, _, _ = doControlReq(t, http.MethodGet, ts.URL+assetPath("tar-tool", drifted.Ability.Digest, "ui/panel.js"), nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("revoked asset GET = %d, want 404", code)
	}

	code, _, body = doControlReq(t, http.MethodDelete, ts.URL+"/api/abilities/tar-tool", nil, abilityUIHeaders())
	if code != http.StatusOK {
		t.Fatalf("delete = %d, body=%s", code, body)
	}
	code, _, _ = doControlReq(t, http.MethodGet, ts.URL+"/api/abilities/tar-tool", nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("GET deleted ability = %d, want 404", code)
	}
}

func TestAbilitiesInstallBodyLimitAndMalformedBodies(t *testing.T) {
	old := maxRequestBody
	maxRequestBody = 32
	defer func() { maxRequestBody = old }()

	h, _, _ := newHub(t)
	if err := h.ConfigureAbilities(t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// Install exceeds 32-byte generic maxRequestBody because guard.go gives /api/abilities/install its own archive cap.
	tgz := makeAbilityTarGz(t, "cap-tool", sampleAbilityFiles("cap-tool"))
	code, _, body := doControlReq(t, http.MethodPost, ts.URL+"/api/abilities/install", bytes.NewReader(tgz), abilityUIHeaders())
	if code != http.StatusCreated {
		t.Fatalf("install with lowered maxRequestBody = %d, body=%s", code, body)
	}
	maxRequestBody = old

	// Malformed bodies across all ability endpoints must return 4xx (never 5xx or 405).
	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/abilities/install"},
		{http.MethodPost, "/api/abilities/scaffold"},
		{http.MethodPost, "/api/abilities/reload"},
		{http.MethodPost, "/api/abilities/cap-tool/trust"},
		{http.MethodPost, "/api/abilities/cap-tool/revoke"},
		{http.MethodPost, "/api/abilities/cap-tool/toggle"},
		{http.MethodPost, "/api/abilities/cap-tool/actions/inspect"},
	}
	badBodies := []string{"{bad json", "[]", `"str"`, "123"}
	for _, ep := range endpoints {
		for _, b := range badBodies {
			code, _, resp := doControlReq(t, ep.method, ts.URL+ep.path, strings.NewReader(b), abilityUIHeaders())
			if code >= 500 || code == http.StatusMethodNotAllowed || code < 400 {
				t.Errorf("%s %s body=%q -> %d (%s), want 4xx (not 405)", ep.method, ep.path, b, code, resp)
			}
		}
	}

	// LocalAddr non-loopback check ensures remote requests without key still get 401.
	req := httptest.NewRequest(http.MethodGet, "http://evil.example/api/abilities", nil)
	ctx := context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 9966})
	rec := httptest.NewRecorder()
	h.Handler().ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("remote unauthenticated /api/abilities = %d, want 401", rec.Code)
	}
}

func jsonMustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
