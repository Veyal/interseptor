package scriptworker

import (
	"time"

	"github.com/Veyal/interseptor/internal/pmsandbox"
	"github.com/Veyal/interseptor/internal/scriptctx"
)

// ProtocolVersion is bumped on incompatible wire changes.
const ProtocolVersion = 1

// Exit codes of the worker process.
const (
	ExitOK     = 0
	ExitMemory = 87 // self-kill after crossing the memory ceiling
	ExitProto  = 88 // protocol violation
)

// Error kinds carried in FrameError and in Output.Errors[].Kind.
const (
	KindMemory  = "memory"
	KindTimeout = "timeout"
	KindCrash   = "error"
	KindProto   = "error"
)

type responseWire struct {
	Code       int                `json:"code"`
	Status     string             `json:"status"`
	Headers    []scriptctx.Header `json:"headers,omitempty"`
	Body       []byte             `json:"body,omitempty"`
	ResponseMs int64              `json:"responseMs"`
	Cookies    []scriptctx.Cookie `json:"cookies,omitempty"`
}

// runWire is the serialisable part of pmsandbox.Input.
type runWire struct {
	V        int                `json:"v"`
	Phase    scriptctx.Phase    `json:"phase"`
	Name     string             `json:"name"`
	Script   string             `json:"script"`
	Request  *scriptctx.Request `json:"request,omitempty"`
	Response *responseWire      `json:"response,omitempty"`
	Vars     scriptctx.Vars     `json:"vars"`
	Cookies  []scriptctx.Cookie `json:"cookies,omitempty"`
	Info     scriptctx.Info     `json:"info"`
	Caps     scriptctx.Caps     `json:"caps"`
	Limits   pmsandbox.Limits   `json:"limits"`
	// ClockNanos pins Date/performance.now (unix nanoseconds); 0 = real time.
	ClockNanos int64 `json:"clockNanos,omitempty"`
	// RandSeed seeds Math.random when HasSeed is set.
	HasSeed  bool   `json:"hasSeed,omitempty"`
	RandSeed uint64 `json:"randSeed,omitempty"`
	// MemLimit is the byte ceiling the worker enforces on itself (0 = none).
	MemLimit uint64 `json:"memLimit,omitempty"`
	HasSend  bool   `json:"hasSend,omitempty"` // parent can service sends
}

type callWire struct {
	ID   uint64                 `json:"id"`
	Send *scriptctx.SendRequest `json:"send,omitempty"`
	URL  string                 `json:"url,omitempty"`
}

type replyWire struct {
	ID   uint64        `json:"id"`
	Err  string        `json:"err,omitempty"`
	Resp *sendRespWire `json:"resp,omitempty"`
}

type sendRespWire struct {
	Code       int                `json:"code"`
	Status     string             `json:"status"`
	Headers    []scriptctx.Header `json:"headers,omitempty"`
	Body       []byte             `json:"body,omitempty"`
	ResponseNs int64              `json:"responseNs"`
}

type errWire struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func toRunWire(in pmsandbox.Input, memLimit uint64) runWire {
	w := runWire{
		V: ProtocolVersion, Phase: in.Phase, Name: in.Name, Script: in.Script,
		Request: in.Request, Vars: in.Vars, Cookies: in.Cookies, Info: in.Info,
		Caps: in.Caps, Limits: in.Limits, MemLimit: memLimit, HasSend: in.Sender != nil,
	}
	if in.Rand != nil {
		w.HasSeed, w.RandSeed = true, uint64(in.Rand()*(1<<53))
	}
	if in.Clock != nil {
		w.ClockNanos = in.Clock().UnixNano()
	}
	if r := in.Response; r != nil {
		w.Response = &responseWire{
			Code: r.Code, Status: r.Status, Headers: r.Headers, Body: r.Body,
			ResponseMs: r.ResponseTime.Milliseconds(), Cookies: r.Cookies,
		}
	}
	return w
}

func (w runWire) response() *scriptctx.Response {
	if w.Response == nil {
		return nil
	}
	r := w.Response
	return &scriptctx.Response{
		Code: r.Code, Status: r.Status, Headers: r.Headers, Body: r.Body,
		ResponseTime: time.Duration(r.ResponseMs) * time.Millisecond, Cookies: r.Cookies,
	}
}
