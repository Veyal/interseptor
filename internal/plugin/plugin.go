// Package plugin is the Phase-1 host registry for Interseptor extensions
// (see docs/product/prd-0005-extensions.md). v1 is in-process only: first-party
// packages register hooks at init/enable time. Third-party load models (UI
// slots, WASM/Starlark host) come in later phases.
//
// Hook signatures deliberately carry only IDs and small scalars — never store
// or proxy types — so extensions cannot create an import cycle and cannot stall
// the proxy hot path. Do any real work (DB reads, network, notifications) in a
// goroutine; emitters run hooks best-effort and never block forwarding.
package plugin

import "sync"

// FlowHook is called after a flow reaches its final recorded state. Best-effort:
// it must not block the proxy hot path — do heavy work in a goroutine.
type FlowHook func(flowID int64)

var (
	mu        sync.Mutex
	flowHooks []FlowHook
)

// OnFlowCaptured registers a flow hook. Safe for concurrent use.
func OnFlowCaptured(h FlowHook) {
	if h == nil {
		return
	}
	mu.Lock()
	flowHooks = append(flowHooks, h)
	mu.Unlock()
}

// invokeHook runs one hook with a panic guard: hooks are called from the capture
// hot path under a best-effort contract, so a buggy extension must not take down
// the proxy process (or skip the hooks registered after it).
func invokeHook(hook func()) {
	defer func() { _ = recover() }()
	hook()
}

// EmitFlowCaptured invokes registered flow hooks outside any caller lock.
func EmitFlowCaptured(flowID int64) {
	mu.Lock()
	hooks := append([]FlowHook(nil), flowHooks...)
	mu.Unlock()
	for _, h := range hooks {
		invokeHook(func() { h(flowID) })
	}
}

// Reset clears all hooks (tests only).
func Reset() {
	mu.Lock()
	flowHooks = nil
	mu.Unlock()
}
