// Package plugin is the Phase-1 host registry for Interseptor extensions
// (see docs/product/prd-0005-extensions.md). v1 is in-process only: first-party
// packages register hooks at init. Third-party load models come later.
package plugin

import "sync"

// FlowHook is called after a flow is persisted (best-effort; must not block
// the proxy hot path — do heavy work in a goroutine).
type FlowHook func(flowID int64)

var (
	mu        sync.Mutex
	flowHooks []FlowHook
)

// OnFlowCaptured registers a hook. Safe for concurrent use.
func OnFlowCaptured(h FlowHook) {
	if h == nil {
		return
	}
	mu.Lock()
	flowHooks = append(flowHooks, h)
	mu.Unlock()
}

// EmitFlowCaptured invokes registered hooks outside any caller lock.
func EmitFlowCaptured(flowID int64) {
	mu.Lock()
	hooks := append([]FlowHook(nil), flowHooks...)
	mu.Unlock()
	for _, h := range hooks {
		h(flowID)
	}
}

// Reset clears hooks (tests only).
func Reset() {
	mu.Lock()
	flowHooks = nil
	mu.Unlock()
}
