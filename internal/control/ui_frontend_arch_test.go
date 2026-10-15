package control

import (
	"regexp"
	"strings"
	"testing"
)

// Issue #105 — event bus, cycle breaking, unified virtualization, event
// delegation and waterfall flattening. These are structural guarantees over the
// shipped JS/HTML; the bus and windowing math themselves are unit-tested under
// node (ui/_js-tests/bus.test.mjs, virtual-list-model.test.mjs).

func TestUIEventBusExistsAndIsPure(t *testing.T) {
	bus := readUIAsset(t, "js/bus.js")
	requireUIContains(t, bus,
		"export function on(event, fn)",
		"export function emit(event, payload)",
		"export function once(event, fn)",
		"export function off(event, fn)",
		"export const EVENTS",
		"export function resetBus()",
	)
	// The bus must stay DOM-free so it loads under node and sits below core.js.
	exec := executableJS(bus)
	for _, forbidden := range []string{"document", "window.", "querySelector", "./core.js"} {
		if strings.Contains(exec, forbidden) {
			t.Errorf("bus.js must stay DOM-free and standalone; found %q", forbidden)
		}
	}
}

func TestUIVirtualListModelIsPure(t *testing.T) {
	model := readUIAsset(t, "js/virtual-list-model.js")
	requireUIContains(t, model, "export function computeVirtualWindow(", "export function sameWindow(")
	exec := executableJS(model)
	for _, forbidden := range []string{"document", "window.", "querySelector", "./core.js"} {
		if strings.Contains(exec, forbidden) {
			t.Errorf("virtual-list-model.js must stay DOM-free; found %q", forbidden)
		}
	}
}

func TestUIUnifiedVirtualizationHelper(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	requireUIContains(t, core,
		"import { computeVirtualWindow, sameWindow } from './virtual-list-model.js'",
		"export function createListView(opts)",
		"e.target.closest", // container-level event delegation
	)
	// Intruder results are folded into the shared helper (its separate hand-rolled
	// virtual list is gone) while keeping the .intr-virt-* classes.
	tools := executableJS(readUIAsset(t, "js/tools.js"))
	requireUIContains(t, tools,
		"createListView({",
		"ensureIntrListView(box).render()",
		"bodyClass:'intr-virt-body'",
	)
	if strings.Contains(tools, "function renderIntrVirtual(") || strings.Contains(tools, "function paintIntrWindow(") {
		t.Error("Intruder must use the shared createListView, not its own hand-rolled virtual list")
	}
	// WebSocket frames: one delegated listener + a bounded render cap, not a
	// per-row rebind of a potentially 10,000-node list.
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, proxy, "function ensureWsFrameDelegation()", "WS_FRAME_RENDER_CAP")
	if strings.Contains(proxy, "function wireWsFrames(") {
		t.Error("WebSocket frames must use delegated activation, not per-row wireWsFrames rebinding")
	}
}

func TestUIForcedReflowGuardsOnScrollPath(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	// applyFlowGrid / syncInspectorVisibility run immediately before computeWindow
	// reads layout on every scroll tick, so they must skip redundant style writes.
	requireUIContains(t, proxy,
		"if(grid===lastFlowGrid)return;",
		"if(insp&&insp.style.display!==inspV)",
	)
}

func TestUIBusBreaksProxyFindingsSettingsCycle(t *testing.T) {
	// The named cycle proxy -> findings -> settings -> proxy is broken by routing
	// the two back-edges over the bus.
	settings := readUIAsset(t, "js/settings.js")
	coreImport := regexp.MustCompile(`(?m)^import .* from './proxy\.js'`)
	if coreImport.MatchString(settings) {
		t.Error("settings.js must not statically import proxy.js (closes the settings->proxy cycle edge)")
	}
	if regexp.MustCompile(`(?m)^import .* from './intercept\.js'`).MatchString(settings) {
		t.Error("settings.js must not statically import intercept.js")
	}
	if regexp.MustCompile(`(?m)^import .* from './checklist\.js'`).MatchString(settings) {
		t.Error("settings.js must reach the checklist through getHook, not a static import")
	}
	requireUIContains(t, settings, "from './bus.js'", "EVENTS.PROXY_RELOAD_FLOWS", "EVENTS.SETTINGS_OPEN_SECTION")

	findings := readUIAsset(t, "js/findings.js")
	if regexp.MustCompile(`(?m)^import .* from './settings\.js'`).MatchString(findings) {
		t.Error("findings.js must not statically import settings.js (closes the findings->settings cycle edge)")
	}
	requireUIContains(t, findings, "EVENTS.SETTINGS_OPEN_SECTION")

	// The panels that own the behaviour listen on the bus.
	requireUIContains(t, executableJS(readUIAsset(t, "js/proxy.js")), "EVENTS.PROXY_RELOAD_FLOWS")
	requireUIContains(t, executableJS(readUIAsset(t, "js/intercept.js")), "EVENTS.INTERCEPT_RELOAD_RULES")
}

func TestUIRootModulesArePreloaded(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, mod := range []string{"core.js", "bus.js", "proxy.js", "settings.js", "findings.js"} {
		if !strings.Contains(index, `<link rel="modulepreload" href="/js/`+mod+`">`) {
			t.Errorf("index.html should modulepreload root module %s to flatten the import waterfall", mod)
		}
	}
}
