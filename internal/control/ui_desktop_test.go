package control

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// Desktop shells (the native macOS app and Chrome/Edge --app windows) run the
// same embedded UI. js/desktop.js detects them and owns the native bridge; the
// pure rules live in js/desktop-model.js and run under node.

func TestUIDesktopModelAndBridgeUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/desktop-model.test.mjs", "_js-tests/desktop-bridge.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --test failed: %v\n%s", err, out)
	}
}

func TestUIDesktopBridgeIsWiredFromAppBoot(t *testing.T) {
	app := readUIAsset(t, "js/app.js")
	requireUIContains(t, app,
		"import { initDesktop, noteDesktopStatus, desktopReady, isDesktopApp } from './desktop.js';",
		"initDesktop({",
		// Commands reuse the shell's own paths, not duplicate fetches.
		"toggleIntercept();return true;", "toggleResponseIntercept();return true;",
		"cmdkOpen();return isCommandPaletteOpen();", "openProjectModal();return true;", "toggleTheme();return true;",
		"activateTab(tab);return true;",
	)
	// ready is posted on both boot outcomes, status on each status source.
	if n := strings.Count(app, "desktopReady();"); n != 2 {
		t.Fatalf("app.js calls desktopReady() %d times, want 2 (hydrated and failed boot)", n)
	}
	for _, src := range []string{"function setTitleProject", "function setSseStatus", "function renderIcptStat"} {
		i := strings.Index(app, src)
		if i < 0 {
			t.Fatalf("app.js lost %s", src)
		}
		body := app[i:]
		if j := strings.Index(body[1:], "\nfunction "); j >= 0 {
			body = body[:j+1]
		}
		if !strings.Contains(body, "noteDesktopStatus()") {
			t.Errorf("%s does not notify the desktop bridge", src)
		}
	}
	requireUIContains(t, readUIAsset(t, "js/intercept.js"),
		"export async function toggleResponseIntercept(){", "$('#respInterceptToggle').onclick=toggleResponseIntercept;")
	// desktop.js stays a leaf: only its pure model, so it can never join a cycle.
	for _, m := range staticImportRE.FindAllStringSubmatch(readUIAsset(t, "js/desktop.js"), -1) {
		if m[1] != "desktop-model.js" {
			t.Errorf("desktop.js imports %s; it may import only desktop-model.js", m[1])
		}
	}
	if staticImportRE.MatchString(readUIAsset(t, "js/desktop-model.js")) {
		t.Error("desktop-model.js must stay import-free")
	}
}

// The native macOS window unifies its title bar with the page's title toolbar:
// the shell hides the native title, hands the page the traffic-light inset and
// drags the window from the regions the page reports; the page reports them
// only for a host that says its title bar is unified.
func TestUIDesktopUnifiedTitleBarContract(t *testing.T) {
	js := readUIAsset(t, "js/desktop.js")
	requireUIContains(t, js,
		"if (w.interseptorDesktop.titlebar === 'unified') installDragRegions();",
		"bar.setAttribute('data-window-drag', '');",
		"post(fileAcceptMessage(input.accept))",
		"areas.push(...uncoveredDragRects(r, clearColumns(el, r)));", "if (isOverlay(r.target)",
	)
	requireUIContains(t, readUIAsset(t, "css/desktop/shell.css"),
		"html.desktop-app .fb-start{padding-left:var(--titlebar-inset-left,0px)}",
		"grid-template-columns:minmax(0,1fr) minmax(180px,360px) minmax(0,1fr)",
	)
	read := func(name string) string {
		b, err := os.ReadFile("../../packaging/macos/shell/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	requireUIContains(t, read("Bridge.swift"), `case "drag-regions":`, `case "file-accept":`, "--titlebar-inset-left", "titlebar:'unified'")
	requireUIContains(t, read("MainWindowController.swift"), ".fullSizeContentView", "window.titleVisibility = .hidden",
		"window.toolbarStyle = .unifiedCompact", "window?.title = t.isEmpty ? \"Interseptor\" : t", "window?.toolbar?.isVisible = false")
	requireUIContains(t, read("WindowDragWebView.swift"), "window.performDrag(with: event)", "AppleActionOnDoubleClick")
	requireUIContains(t, read("WebUIDelegate.swift"), "panel.allowedContentTypes = types")
}

func TestUIDesktopStylesAreScopedToDesktopHosts(t *testing.T) {
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index,
		`<meta name="theme-color" content="#171a20">`,
		`<link rel="manifest" href="/manifest.webmanifest" crossorigin="use-credentials">`,
		`<link rel="stylesheet" href="/desktop.css">`+"\n"+`<link rel="stylesheet" href="/mobile.css">`,
	)
	css := stripCSSComments(readUIAsset(t, "desktop.css"))
	rules := regexp.MustCompile(`([^{}]+)\{[^{}]*\}`).FindAllStringSubmatch(css, -1)
	if len(rules) < 5 {
		t.Fatalf("desktop.css has %d rules", len(rules))
	}
	for _, r := range rules {
		for _, sel := range splitTopLevelCommas(r[1]) {
			if sel = strings.TrimSpace(sel); !strings.HasPrefix(sel, "html.desktop-app") {
				t.Errorf("desktop.css selector %q is not scoped under html.desktop-app; a browser tab would change", sel)
			}
		}
	}
	// The app-feel contract.
	for _, want := range []string{"overscroll-behavior:none", "cursor:default!important", "cursor:text", "user-select:none", "user-select:text", "-webkit-user-drag:none", "-webkit-tap-highlight-color:transparent"} {
		if !strings.Contains(css, want) {
			t.Errorf("desktop.css lacks %s", want)
		}
	}
	// Busy controls keep their progress cursor and real links keep the pointer.
	requireUIContains(t, css, `:not([aria-busy="true"]):not(.is-pending)`, "a.btn")
	if strings.Contains(css, "a[href]") {
		t.Error("desktop.css must leave real hyperlinks (a[href]) on the pointer cursor")
	}
	// Request/response bodies and editors are never made unselectable.
	for _, id := range []string{"#reqView", "#resView", "#repBody", "#heldRaw", "#fdBody"} {
		if strings.Contains(css, id) {
			t.Errorf("desktop.css styles %s; message bodies must stay selectable", id)
		}
	}
	// Desktop guards and classes are added only when a host is detected.
	js := readUIAsset(t, "js/desktop.js")
	requireUIContains(t, js, "if (desktop) {\n    document.documentElement.classList.add(...desktop.classes);\n    installGuards();",
		"'(display-mode: standalone)'", "{ passive: false }")
}

// splitTopLevelCommas splits a selector list on commas outside parentheses.
func splitTopLevelCommas(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, c := range s {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

func TestServeUIWebManifest(t *testing.T) {
	rec := uiGet(t, "/manifest.webmanifest", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/manifest+json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("ETag %q Cache-Control %q, want the shared asset validators", etag, rec.Header().Get("Cache-Control"))
	}
	if again := uiGet(t, "/manifest.webmanifest", map[string]string{"If-None-Match": etag}); again.Code != http.StatusNotModified {
		t.Fatalf("revalidation status = %d, want 304", again.Code)
	}
	var m struct {
		Name            string `json:"name"`
		ShortName       string `json:"short_name"`
		StartURL        string `json:"start_url"`
		Display         string `json:"display"`
		BackgroundColor string `json:"background_color"`
		ThemeColor      string `json:"theme_color"`
		Icons           []struct {
			Src  string `json:"src"`
			Type string `json:"type"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	if m.Name != "Interseptor" || m.ShortName == "" || m.StartURL != "/" || m.Display != "standalone" {
		t.Fatalf("manifest identity = %+v", m)
	}
	hex := regexp.MustCompile(`^#[0-9a-f]{6}$`)
	if !hex.MatchString(m.BackgroundColor) || !hex.MatchString(m.ThemeColor) {
		t.Fatalf("manifest colours %q %q", m.BackgroundColor, m.ThemeColor)
	}
	if len(m.Icons) == 0 {
		t.Fatal("manifest has no icon")
	}
	for _, icon := range m.Icons {
		got := uiGet(t, icon.Src, nil)
		if got.Code != http.StatusOK || got.Header().Get("Content-Type") != icon.Type {
			t.Errorf("icon %s: status %d type %q, want 200 %s", icon.Src, got.Code, got.Header().Get("Content-Type"), icon.Type)
		}
		if svg := got.Body.String(); !strings.Contains(svg, "<svg") || strings.Contains(strings.ReplaceAll(svg, "http://www.w3.org/2000/svg", ""), "://") {
			t.Errorf("icon %s is not a self-contained SVG", icon.Src)
		}
	}
}
