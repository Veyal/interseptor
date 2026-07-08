package autopwn

import (
	"context"
	"io"

	"github.com/Veyal/interseptor/internal/aiagent"
	"github.com/Veyal/interseptor/internal/mcp"
	"github.com/Veyal/interseptor/internal/oob"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/verify"
)

// senderAdapter couples the verifier's verify.Sender interface to the real
// internal/sender.Sender. Every re-sent variant becomes a real recorded flow
// (tagged FlagAI) whose id is returned for PoC attachment. The response body is
// loaded back from the content-addressed store so the deterministic oracles can
// inspect it — sender.Send only returns body hashes on the flow.
type senderAdapter struct {
	st *store.Store
	sn *sender.Sender
}

// newSenderAdapter wraps a store+sender as a verify.Sender.
func newSenderAdapter(st *store.Store, sn *sender.Sender) *senderAdapter {
	return &senderAdapter{st: st, sn: sn}
}

// Send maps a verify.Request onto a sender.Request, issues it (recording a real
// flow tagged FlagAI), and maps the resulting flow back to a verify.Exchange —
// resolving the response body from the store by hash. ctx cancels an in-flight
// send.
func (a *senderAdapter) Send(ctx context.Context, req verify.Request) verify.Exchange {
	flow, err := a.sn.Send(sender.Request{
		Method:  req.Method,
		URL:     req.URL,
		Headers: req.Headers,
		Body:    req.Body,
		Flags:   store.FlagAI,
		Context: ctx,
	})
	if err != nil {
		return verify.Exchange{Err: err}
	}
	ex := verify.Exchange{
		Status:  flow.Status,
		Headers: flow.ResHeaders,
		DurMs:   flow.DurationMs,
		FlowID:  flow.ID,
	}
	if flow.Error != "" {
		ex.Err = errString(flow.Error)
	}
	if body := a.loadBody(flow.ResBodyHash); body != nil {
		ex.Body = body
	}
	return ex
}

// loadBody reads the response body for a hash, best-effort (nil on any miss).
func (a *senderAdapter) loadBody(hash string) []byte {
	if hash == "" || a.st == nil {
		return nil
	}
	rc, err := a.st.OpenBody(hash)
	if err != nil {
		return nil
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil
	}
	return b
}

// errString is a tiny error type so a flow-level transport error (recorded as a
// string on the flow) can be surfaced as a verify.Exchange.Err.
type errString string

func (e errString) Error() string { return string(e) }

// oobAdapter couples the verifier's verify.OOBPoller to the real oob.Catcher.
// A token has "hits" when the catcher has recorded any interaction carrying it —
// a callback to the globally-unique URL only we injected is ground-truth proof.
type oobAdapter struct{ c *oob.Catcher }

// newOOBAdapter wraps an oob.Catcher as a verify.OOBPoller.
func newOOBAdapter(c *oob.Catcher) *oobAdapter { return &oobAdapter{c: c} }

// HitsForToken counts recorded interactions for the exact token.
func (a *oobAdapter) HitsForToken(token string) int {
	if a.c == nil {
		return 0
	}
	return len(a.c.InteractionsForToken(token))
}

// toolExecutor couples aiagent.ToolExecutor to mcp.Server.Call, giving the
// planning and verifier agents in-process access to all ~84 tools (each carrying
// FlagAI History tagging + Activity logging). ctx is honored cooperatively: a
// cancelled ctx short-circuits before the (localhost) tool hop.
type toolExecutor struct{ srv *mcp.Server }

// newToolExecutor wraps an mcp.Server as an aiagent.ToolExecutor.
func newToolExecutor(srv *mcp.Server) *toolExecutor { return &toolExecutor{srv: srv} }

// Exec runs one tool call by name against the in-process tool bus.
func (e *toolExecutor) Exec(ctx context.Context, call aiagent.ToolCall) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if e.srv == nil {
		return "", errString("autopwn: no tool server configured")
	}
	return e.srv.Call(call.Name, call.Args)
}
