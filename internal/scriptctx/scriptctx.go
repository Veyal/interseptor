// Package scriptctx defines the data exchanged between the collection
// pipeline and the script sandbox (internal/pmsandbox): what a script may see
// (request, response, variables, cookies, info) and what it may change or
// report (variable writes, request edits, tests, flow control).
//
// It holds types only and imports nothing from the rest of the project, so
// the pipeline and the sandbox can both depend on it without cycles.
package scriptctx

import (
	"context"
	"time"
)

// Phase is the script event the sandbox runs.
type Phase string

const (
	PhasePreRequest Phase = "prerequest"
	PhaseTest       Phase = "test"
)

// Test result statuses. StatusUnsupported is distinct from StatusFail so CI
// never mistakes an unimplemented Postman API for a real assertion failure.
const (
	StatusPass        = "pass"
	StatusFail        = "fail"
	StatusSkip        = "skip"
	StatusError       = "error"
	StatusUnsupported = "unsupported"
)

// Variable scope names, as used in VarChange.Scope.
const (
	ScopeEnvironment = "environment"
	ScopeGlobals     = "globals"
	ScopeCollection  = "collection"
	ScopeLocal       = "local"
)

// Header is one ordered header pair.
type Header struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled,omitempty"`
}

// Param is a urlencoded or form-data body field.
type Param struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Type     string `json:"type,omitempty"` // text|file
	Disabled bool   `json:"disabled,omitempty"`
}

// Body is a request body. Mode is raw|urlencoded|formdata|graphql|none|file.
type Body struct {
	Mode       string  `json:"mode,omitempty"`
	Raw        string  `json:"raw,omitempty"`
	Language   string  `json:"language,omitempty"`
	URLEncoded []Param `json:"urlencoded,omitempty"`
	FormData   []Param `json:"formdata,omitempty"`
}

// Auth carries the request's auth config. Type is none|bearer|basic|apikey|
// noauth; Params holds the type-specific fields.
type Auth struct {
	Type   string            `json:"type,omitempty"`
	Params map[string]string `json:"params,omitempty"`
}

// Request is the script-visible, script-mutable request (pre-request phase)
// and the read-only request that produced the response (test phase).
type Request struct {
	Name    string   `json:"name,omitempty"`
	ID      string   `json:"id,omitempty"`
	Method  string   `json:"method"`
	URL     string   `json:"url"`
	Headers []Header `json:"headers,omitempty"`
	Body    Body     `json:"body,omitempty"`
	Auth    *Auth    `json:"auth,omitempty"`
}

// Response is the script-visible response (test phase).
type Response struct {
	Code         int           `json:"code"`
	Status       string        `json:"status"`
	Headers      []Header      `json:"headers,omitempty"`
	Body         []byte        `json:"-"`
	ResponseTime time.Duration `json:"-"`
	Cookies      []Cookie      `json:"cookies,omitempty"`
}

// Cookie is a jar entry.
type Cookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain,omitempty"`
	Path     string `json:"path,omitempty"`
	Expires  string `json:"expires,omitempty"`
	HTTPOnly bool   `json:"httpOnly,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
}

// Vars are the variable scopes visible to a script. Values are JSON types
// (usually strings). Secret lists names whose current values must never
// appear in console output, test messages or errors.
type Vars struct {
	Environment   map[string]any `json:"environment,omitempty"`
	Globals       map[string]any `json:"globals,omitempty"`
	Collection    map[string]any `json:"collection,omitempty"`
	Local         map[string]any `json:"local,omitempty"`
	IterationData map[string]any `json:"iterationData,omitempty"`
	EnvName       string         `json:"envName,omitempty"`
	Secret        []string       `json:"secret,omitempty"`
}

// Info feeds pm.info.
type Info struct {
	EventName      string `json:"eventName,omitempty"`
	Iteration      int    `json:"iteration"`
	IterationCount int    `json:"iterationCount"`
	RequestName    string `json:"requestName,omitempty"`
	RequestID      string `json:"requestId,omitempty"`
}

// Caps are the default-deny capabilities granted to a script (plan section
// 11). The zero value grants nothing; use DefaultCaps for a trusted script.
type Caps struct {
	VarsRead     bool `json:"varsRead"`
	VarsWrite    bool `json:"varsWrite"`
	CookiesRead  bool `json:"cookiesRead"`
	CookiesWrite bool `json:"cookiesWrite"`
	NetSend      bool `json:"netSend"`
	SecretsRead  bool `json:"secretsRead"`
}

// DefaultCaps is what a trusted script gets: variables, cookies, scoped
// sends and secret reads (findings, identity, oob and out-of-scope stay off).
func DefaultCaps() Caps {
	return Caps{VarsRead: true, VarsWrite: true, CookiesRead: true, CookiesWrite: true, NetSend: true, SecretsRead: true}
}

// SendRequest is a pm.sendRequest call after the sandbox normalized it.
type SendRequest struct {
	Method  string
	URL     string
	Headers []Header
	Body    Body
}

// SendResponse is what pm.sendRequest hands back to the script.
type SendResponse struct {
	Code         int
	Status       string
	Headers      []Header
	Body         []byte
	ResponseTime time.Duration
}

// Sender performs a script-initiated send. The implementation is the proxied
// sender behind the scope guard; the sandbox never dials anything itself.
type Sender interface {
	Send(ctx context.Context, req SendRequest) (SendResponse, error)
}

// SendFunc adapts a function to Sender.
type SendFunc func(ctx context.Context, req SendRequest) (SendResponse, error)

// Send implements Sender.
func (f SendFunc) Send(ctx context.Context, r SendRequest) (SendResponse, error) { return f(ctx, r) }

// TestResult is one assertion outcome (plan section 6).
type TestResult struct {
	Name     string        `json:"name"`
	Status   string        `json:"status"`
	Message  string        `json:"message,omitempty"`
	Expected string        `json:"expected,omitempty"`
	Actual   string        `json:"actual,omitempty"`
	Source   string        `json:"source,omitempty"` // script name:line:col when known
	Duration time.Duration `json:"duration,omitempty"`
}

// VarChange records one variable write for the persist policy.
type VarChange struct {
	Scope string `json:"scope"`
	Name  string `json:"name"`
	Op    string `json:"op"` // set|unset|clear
	Value any    `json:"value,omitempty"`
}

// CookieOp records a pm.cookies.jar() write.
type CookieOp struct {
	Op     string `json:"op"` // set|clear
	URL    string `json:"url,omitempty"`
	Cookie Cookie `json:"cookie"`
}

// SendRecord is the audit line for one pm.sendRequest.
type SendRecord struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Code   int    `json:"code,omitempty"`
	Error  string `json:"error,omitempty"`
}

// FlowControl is the setNextRequest / skipRequest outcome.
type FlowControl struct {
	NextSet bool   `json:"nextSet,omitempty"`
	Next    string `json:"next,omitempty"` // empty with NextSet = stop the run
	Skip    bool   `json:"skip,omitempty"`
}

// ConsoleLine is one scrubbed console call.
type ConsoleLine struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}
