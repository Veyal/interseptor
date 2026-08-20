package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
	flow, err := h.snd.Send(sender.Request{
		Method:  in.Method,
		URL:     in.URL,
		Host:    host,
		Headers: hdr,
		Body:    []byte(body),
		Flags:   store.FlagRepeater | aiSourceFlag(r),
	})
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.flowDetail(flow))
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
