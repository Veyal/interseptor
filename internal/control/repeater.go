package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/httplines"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

type repeaterSendJSON struct {
	Method   string          `json:"method"`
	URL      string          `json:"url"`
	Headers  json.RawMessage `json:"headers"` // "Key: Value" lines or {"Key":"Value"} object
	Body     string          `json:"body"`
	BodyMode string          `json:"bodyMode"` // ""|"raw" (default) or "decoded"
	CodecID  string          `json:"codecId"`  // required when bodyMode=decoded
	FlowID   int64           `json:"flowId"`   // optional context for encode()
	RawBody  string          `json:"rawBody"`  // wire body for encode context (prefix/fields)

	// Replay options (sender.SendOptions). All omitted -> nil options, i.e. the
	// historical Repeater behaviour: no redirects, no TLS verification, HTTP/1.1.
	FollowRedirects bool   `json:"followRedirects"`
	MaxRedirects    int    `json:"maxRedirects"` // 0 -> 10 when following
	VerifyTLS       bool   `json:"verifyTls"`    // verify the origin certificate
	ServerName      string `json:"serverName"`   // SNI / verification name override
	HTTP2           bool   `json:"http2"`        // offer ALPN h2 (HTTPS only)
	TimeoutMs       int    `json:"timeoutMs"`    // whole-exchange timeout; 0 -> defaultSendTimeout
}

// repeaterOptions builds the per-send options, or nil when every option is at
// its default so the send takes the exact historical client path (byte for byte
// the pre-existing Repeater behaviour).
func repeaterOptions(in repeaterSendJSON) *sender.SendOptions {
	if !in.FollowRedirects && !in.VerifyTLS && !in.HTTP2 && in.ServerName == "" && in.TimeoutMs <= 0 {
		return nil
	}
	o := &sender.SendOptions{
		FollowRedirects: in.FollowRedirects,
		MaxRedirects:    in.MaxRedirects,
		VerifyTLS:       in.VerifyTLS,
		ServerName:      in.ServerName,
		HTTP2:           in.HTTP2,
	}
	if in.TimeoutMs > 0 {
		o.Timeout = time.Duration(in.TimeoutMs) * time.Millisecond
	}
	return o
}

// defaultSendTimeout bounds a Repeater, replay or retest send whose caller set
// no timeoutMs, so a hung origin cannot pin a handler and an upstream
// connection forever. It is generous on purpose: slow endpoints still finish.
var defaultSendTimeout = 5 * time.Minute

// sendContext ties a control-plane send to the HTTP request that asked for it:
// a client that disconnects or clicks Cancel aborts the upstream exchange. When
// the caller set no explicit timeout the default above applies; an explicit
// timeoutMs is enforced by the sender itself and may be longer than the default.
func sendContext(r *http.Request, timeoutMs int) (context.Context, context.CancelFunc) {
	if timeoutMs > 0 {
		return context.WithCancel(r.Context())
	}
	return context.WithTimeout(r.Context(), defaultSendTimeout)
}

// aiSourceFlag returns store.FlagAI when a request was issued by the AI assistant
// over MCP — the MCP server stamps every control call with X-Interseptor-Source:
// ai. It lets the control plane distinguish AI-originated Repeater/Intruder/scan
// sends from a human's and surface them in Proxy/History.
func aiSourceFlag(r *http.Request) int64 {
	if strings.EqualFold(r.Header.Get("X-Interseptor-Source"), "ai") {
		return store.FlagAI
	}
	return 0
}

func (h *toolsAPI) repeaterSend(w http.ResponseWriter, r *http.Request) {
	var in repeaterSendJSON
	if !decodeLimitedJSON(w, r, maxRequestBody, &in) {
		return
	}
	if h.targetsOwnListener(in.URL) {
		httpErr(w, http.StatusForbidden, "refusing to send to Interseptor's own listener")
		return
	}
	hdr, err := httplines.NormalizeJSON(in.Headers)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	host, hdr := splitHostHeader(hdr)
	body := in.Body
	if strings.EqualFold(in.BodyMode, "decoded") {
		if in.CodecID == "" {
			httpErr(w, http.StatusBadRequest, "codecId required when bodyMode=decoded")
			return
		}
		wire, err := h.encodeWithCodec(in.CodecID, in.FlowID, "req", in.Body, in.RawBody)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		body = wire
	}
	ctx, cancel := sendContext(r, in.TimeoutMs)
	defer cancel()
	flow, err := h.snd.Send(sender.Request{
		Method:  in.Method,
		URL:     in.URL,
		Host:    host,
		Headers: hdr,
		Body:    []byte(body),
		Flags:   store.FlagRepeater | aiSourceFlag(r),
		Options: repeaterOptions(in),
		Context: ctx,
	})
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.flowDetail(flow))
}

// repeaterParamsParse splits a URL's query string and an optional
// x-www-form-urlencoded body into ordered fields, preserving order and
// duplicate keys so the Repeater "Params" drawer can round-trip them.
func (h *toolsAPI) repeaterParamsParse(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL  string `json:"url"`
		Body string `json:"body"`
		Form bool   `json:"form"` // parse Body as application/x-www-form-urlencoded
	}
	if !decodeLimitedJSON(w, r, maxRequestBody, &in) {
		return
	}
	base, query := splitURLQuery(in.URL)
	out := map[string]any{"base": base, "query": orEmptyParams(sender.ParseParams(query))}
	if in.Form {
		out["form"] = orEmptyParams(sender.ParseParams(in.Body))
	}
	writeJSON(w, http.StatusOK, out)
}

// repeaterParamsBuild rebuilds a URL (base + query string) and a form body from
// ordered fields — the inverse of repeaterParamsParse.
func (h *toolsAPI) repeaterParamsBuild(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Base  string         `json:"base"`
		URL   string         `json:"url"` // alias for base; its own query is dropped
		Query []sender.Param `json:"query"`
		Form  []sender.Param `json:"form"`
	}
	if !decodeLimitedJSON(w, r, maxRequestBody, &in) {
		return
	}
	base := in.Base
	if base == "" {
		base = in.URL
	}
	base, _ = splitURLQuery(base)
	url := base
	if q := sender.BuildParams(in.Query); q != "" {
		url += "?" + q
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": url, "body": sender.BuildParams(in.Form)})
}

// splitURLQuery returns the URL without its query or fragment, and the raw query
// string. A fragment is dropped: it is never sent on the wire.
func splitURLQuery(raw string) (base, query string) {
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		return raw[:i], raw[i+1:]
	}
	return raw, ""
}

// orEmptyParams returns a non-nil slice so the JSON field is [] not null.
func orEmptyParams(p []sender.Param) []sender.Param {
	if p == nil {
		return []sender.Param{}
	}
	return p
}

// splitHostHeader keeps the Repeater's connection URL independent from the
// wire-level Host header. A Host override is useful for vhost and Host-header
// injection testing, but it must not retarget the request connection.
func splitHostHeader(headers map[string][]string) (string, map[string][]string) {
	if len(headers) == 0 {
		return "", headers
	}
	host := ""
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		if strings.EqualFold(key, "Host") {
			if host == "" && len(values) > 0 {
				host = values[0]
			}
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	return host, out
}

func (h *toolsAPI) repeaterHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	endpoint, err := repeaterHistoryEndpoint(q)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := repeaterHistoryLimit(q.Get("limit"))
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}

	filter := store.FlowFilter{
		RequireFlags: store.FlagRepeater,
		Limit:        limit,
	}
	if endpoint != nil {
		filter.EndpointScheme = endpoint.scheme
		filter.EndpointHost = endpoint.host
		filter.EndpointPort = endpoint.port
		filter.EndpointPath = endpoint.path
	}
	flows, err := h.st.QueryFlowsListFilter(filter)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	out := make([]flowJSON, 0, len(flows))
	for _, f := range flows {
		out = append(out, toFlowJSON(f))
	}
	writeJSON(w, http.StatusOK, map[string]any{"flows": out})
}

func repeaterHistoryLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 100, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 5000 {
		return 0, fmt.Errorf("limit must be between 1 and 5000")
	}
	return limit, nil
}

// repeaterEndpoint is the stable identity used by Repeater tabs. Query
// parameters are intentionally excluded: sending the same resource with a
// different query still belongs to that tab's endpoint history.
type repeaterEndpoint struct {
	scheme string
	host   string
	port   int
	path   string
}

// repeaterHistoryEndpoint accepts the canonical endpoint parameter and its
// component form. The latter keeps the endpoint contract easy for clients that
// already have flow metadata. An absent endpoint filter returns nil so the
// legacy global history response remains unchanged.
func repeaterHistoryEndpoint(q url.Values) (*repeaterEndpoint, error) {
	hasEndpoint := q.Has("endpoint")
	hasComponents := q.Has("scheme") || q.Has("host") || q.Has("port") || q.Has("path")
	if hasEndpoint && hasComponents {
		return nil, fmt.Errorf("use endpoint or its components, not both")
	}
	if hasEndpoint {
		raw := strings.TrimSpace(q.Get("endpoint"))
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Hostname() == "" || u.User != nil {
			return nil, fmt.Errorf("endpoint must be an absolute URL")
		}
		scheme, err := repeaterEndpointScheme(u.Scheme)
		if err != nil {
			return nil, err
		}
		port, err := endpointPort(u)
		if err != nil {
			return nil, err
		}
		return &repeaterEndpoint{
			scheme: scheme,
			host:   strings.ToLower(u.Hostname()),
			port:   port,
			path:   querylessPath(u.EscapedPath()),
		}, nil
	}
	if !hasComponents {
		return nil, nil
	}
	if !q.Has("scheme") || !q.Has("host") || !q.Has("port") || !q.Has("path") {
		return nil, fmt.Errorf("endpoint scheme, host, port, and path are required")
	}
	scheme, err := repeaterEndpointScheme(q.Get("scheme"))
	if err != nil {
		return nil, err
	}
	host := strings.ToLower(strings.TrimSpace(q.Get("host")))
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "" {
		return nil, fmt.Errorf("endpoint host is required")
	}
	port, err := strconv.Atoi(strings.TrimSpace(q.Get("port")))
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("endpoint port must be between 1 and 65535")
	}
	path := strings.TrimSpace(q.Get("path"))
	if path == "" {
		return nil, fmt.Errorf("endpoint path is required")
	}
	endpoint := &repeaterEndpoint{
		scheme: scheme,
		host:   host,
		port:   port,
		path:   querylessPath(path),
	}
	return endpoint, nil
}

func repeaterEndpointScheme(raw string) (string, error) {
	scheme := strings.ToLower(strings.TrimSpace(raw))
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("endpoint scheme must be http or https")
	}
	return scheme, nil
}

func endpointPort(u *url.URL) (int, error) {
	if p := u.Port(); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil || port < 1 || port > 65535 {
			return 0, fmt.Errorf("endpoint port must be between 1 and 65535")
		}
		return port, nil
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return 80, nil
	case "https":
		return 443, nil
	default:
		return 0, fmt.Errorf("endpoint scheme must be http or https")
	}
}

func querylessPath(path string) string {
	if path == "" {
		return "/"
	}
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	if i := strings.IndexByte(path, '#'); i >= 0 {
		path = path[:i]
	}
	if path == "" {
		return "/"
	}
	return path
}

// flowDetail builds the detail DTO for a freshly-sent flow.
func (h *toolsAPI) flowDetail(f *store.Flow) flowDetailJSON {
	return flowDetailJSON{
		flowJSON:    toFlowJSON(f),
		HTTPVersion: f.HTTPVersion,
		ReqHeaders:  f.ReqHeaders,
		ResHeaders:  f.ResHeaders,
		ReqBodyHash: f.ReqBodyHash,
		ResBodyHash: f.ResBodyHash,
	}
}

// parseHeaderLines turns a raw "Key: Value" block into a header map.
func parseHeaderLines(s string) map[string][]string {
	h := map[string][]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" {
			continue
		}
		h[k] = append(h[k], v)
	}
	return h
}
