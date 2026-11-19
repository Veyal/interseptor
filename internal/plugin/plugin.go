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

type flowHookEntry struct {
	id uint64
	fn FlowHook
}

var (
	mu         sync.Mutex
	nextHookID uint64
	flowHooks  []flowHookEntry
)

// OnFlowCaptured registers a flow hook. Safe for concurrent use.
func OnFlowCaptured(h FlowHook) {
	RegisterFlowHook(h)
}

// RegisterFlowHook registers a flow hook and returns an idempotent cancel
// function that unregisters it. Safe for concurrent use.
func RegisterFlowHook(h FlowHook) func() {
	if h == nil {
		return func() {}
	}
	mu.Lock()
	nextHookID++
	id := nextHookID
	flowHooks = append(flowHooks, flowHookEntry{id: id, fn: h})
	mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			for i, e := range flowHooks {
				if e.id == id {
					flowHooks = append(flowHooks[:i], flowHooks[i+1:]...)
					break
				}
			}
			mu.Unlock()
		})
	}
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
	entries := append([]flowHookEntry(nil), flowHooks...)
	mu.Unlock()
	for _, e := range entries {
		invokeHook(func() { e.fn(flowID) })
	}
}

// Reset clears all hooks (tests only).
func Reset() {
	mu.Lock()
	flowHooks = nil
	nextHookID = 0
	mu.Unlock()
}
