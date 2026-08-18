// Package burpx streams Burp Suite "Save items" XML exports into normalized
// HTTP entries. Native .burp project files use Burp's private persistence
// format and are deliberately not reverse-engineered here.
package burpx

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/netutil"
)

// Entry is one Burp request/response pair normalized for import.
type Entry struct {
	Method      string
	URL         string
	HTTPVersion string
	ReqHeaders  http.Header
	ReqBody     []byte
	Status      int
	ResHeaders  http.Header
	ResBody     []byte
	Mime        string
	TS          time.Time
	Comment     string
}

type rawMessage struct {
	Base64 string `xml:"base64,attr"`
	Data   string `xml:",chardata"`
}

type rawItem struct {
	Time     string     `xml:"time"`
	URL      string     `xml:"url"`
	Host     string     `xml:"host"`
	Port     int        `xml:"port"`
	Protocol string     `xml:"protocol"`
	Method   string     `xml:"method"`
	Path     string     `xml:"path"`
	Request  rawMessage `xml:"request"`
	Status   int        `xml:"status"`
	Mime     string     `xml:"mimetype"`
	Response rawMessage `xml:"response"`
	Comment  string     `xml:"comment"`
}

// Parse streams a Burp "Save items" XML document and visits each item. The
// decoder retains at most one exported item at a time, rather than buffering the
// complete history export in memory.
func Parse(r io.Reader, visit func(Entry) error) (int, error) {
	br := bufio.NewReader(r)
	prefix, _ := br.Peek(64)
	if trimmed := bytes.TrimSpace(prefix); len(trimmed) > 0 && trimmed[0] != '<' {
		return 0, fmt.Errorf("native .burp project files are not a documented interchange format; select HTTP items in Burp, choose Save items, and export XML")
	}

	dec := xml.NewDecoder(br)
	foundRoot := false
	count := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, fmt.Errorf("parse Burp saved-items XML: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if !foundRoot {
			if start.Name.Local != "items" {
				return count, fmt.Errorf("not Burp saved-items XML: root element is <%s>, want <items>", start.Name.Local)
			}
			foundRoot = true
			continue
		}
		if start.Name.Local != "item" {
			continue
		}
		var raw rawItem
		if err := dec.DecodeElement(&raw, &start); err != nil {
			return count, fmt.Errorf("decode Burp item %d: %w", count+1, err)
		}
		entry, err := normalizeItem(raw)
		if err != nil {
			return count, fmt.Errorf("decode Burp item %d: %w", count+1, err)
		}
		if visit != nil {
			if err := visit(entry); err != nil {
				return count, err
			}
		}
		count++
	}
	if !foundRoot {
		return 0, fmt.Errorf("not Burp saved-items XML: missing <items> root")
	}
	return count, nil
}

func normalizeItem(raw rawItem) (Entry, error) {
	reqRaw, err := decodeMessage(raw.Request)
	if err != nil {
		return Entry{}, fmt.Errorf("request base64: %w", err)
	}
	resRaw, err := decodeMessage(raw.Response)
	if err != nil {
		return Entry{}, fmt.Errorf("response base64: %w", err)
	}

	method, path, proto, reqHeaders, reqBody, err := parseRequest(reqRaw)
	if err != nil {
		return Entry{}, fmt.Errorf("request: %w", err)
	}
	status, resProto, resHeaders, resBody, err := parseResponse(resRaw)
	if err != nil {
		return Entry{}, fmt.Errorf("response: %w", err)
	}
	if method == "" {
		method = strings.TrimSpace(raw.Method)
	}
	if path == "" {
		path = strings.TrimSpace(raw.Path)
	}
	if path == "" {
		path = "/"
	}
	itemURL := strings.TrimSpace(raw.URL)
	if itemURL == "" {
		itemURL = buildURL(raw.Protocol, raw.Host, raw.Port, path)
	}
	if status == 0 {
		status = raw.Status
	}
	if proto == "" {
		proto = resProto
	}
	mime := resHeaders.Get("Content-Type")
	if mime == "" {
		mime = strings.TrimSpace(raw.Mime)
	}
	return Entry{
		Method: method, URL: itemURL, HTTPVersion: proto,
		ReqHeaders: reqHeaders, ReqBody: reqBody,
		Status: status, ResHeaders: resHeaders, ResBody: resBody,
		Mime: mime, TS: parseBurpTime(raw.Time), Comment: strings.TrimSpace(raw.Comment),
	}, nil
}

func decodeMessage(msg rawMessage) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(msg.Base64)) {
	case "", "false":
		return []byte(msg.Data), nil
	case "true":
		data := strings.TrimSpace(msg.Data)
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("invalid base64 attribute %q", msg.Base64)
	}
}

func parseRequest(raw []byte) (method, path, proto string, headers http.Header, body []byte, err error) {
	if len(raw) == 0 {
		return "", "", "", http.Header{}, nil, nil
	}
	head, body := splitMessage(raw)
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(normalizeHead(head))))
	if err != nil {
		return "", "", "", nil, nil, err
	}
	headers = req.Header.Clone()
	if headers == nil {
		headers = http.Header{}
	}
	if req.Host != "" {
		headers.Set("Host", req.Host)
	}
	body, err = normalizeTransferBody(body, req.TransferEncoding)
	if err != nil {
		return "", "", "", nil, nil, err
	}
	setCanonicalContentLength(headers, body)
	path = req.URL.RequestURI()
	if path == "" {
		path = "/"
	}
	return req.Method, path, req.Proto, headers, body, nil
}

func parseResponse(raw []byte) (status int, proto string, headers http.Header, body []byte, err error) {
	if len(raw) == 0 {
		return 0, "", http.Header{}, nil, nil
	}
	head, body := splitMessage(raw)
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(normalizeHead(head))), nil)
	if err != nil {
		return 0, "", nil, nil, err
	}
	_ = resp.Body.Close()
	headers = resp.Header.Clone()
	body, err = normalizeTransferBody(body, resp.TransferEncoding)
	if err != nil {
		return 0, "", nil, nil, err
	}
	setCanonicalContentLength(headers, body)
	return resp.StatusCode, resp.Proto, headers, body, nil
}

func normalizeTransferBody(body []byte, encodings []string) ([]byte, error) {
	chunked := false
	for _, encoding := range encodings {
		if strings.EqualFold(strings.TrimSpace(encoding), "chunked") {
			chunked = true
			break
		}
	}
	if !chunked {
		return body, nil
	}
	decoded, err := io.ReadAll(httputil.NewChunkedReader(bytes.NewReader(body)))
	if err != nil {
		return nil, fmt.Errorf("decode chunked body: %w", err)
	}
	return decoded, nil
}

func setCanonicalContentLength(headers http.Header, body []byte) {
	headers.Del("Transfer-Encoding")
	if len(body) > 0 {
		headers.Set("Content-Length", strconv.Itoa(len(body)))
	} else {
		headers.Del("Content-Length")
	}
}

func splitMessage(raw []byte) (head, body []byte) {
	if i := bytes.Index(raw, []byte{13, 10, 13, 10}); i >= 0 {
		return raw[:i], raw[i+4:]
	}
	if i := bytes.Index(raw, []byte{10, 10}); i >= 0 {
		return raw[:i], raw[i+2:]
	}
	return raw, nil
}

func normalizeHead(head []byte) []byte {
	s := strings.ReplaceAll(strings.ReplaceAll(string(head), "\r\n", "\n"), "\n", "\r\n")
	return []byte(strings.TrimRight(s, "\r\n") + "\r\n\r\n")
}

func buildURL(scheme, host string, port int, path string) string {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme == "" {
		if port == 443 {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	u := url.URL{Scheme: scheme, Host: netutil.URLHost(scheme, host, port), Path: "/"}
	if parsed, err := url.ParseRequestURI(path); err == nil {
		u.Path, u.RawPath, u.RawQuery = parsed.Path, parsed.RawPath, parsed.RawQuery
	}
	return u.String()
}

func parseBurpTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{
		time.RFC3339Nano,
		"Mon Jan 02 15:04:05 MST 2006",
		"Mon Jan 2 15:04:05 MST 2006",
	} {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts
		}
	}
	return time.Time{}
}
