package collexec

import (
	"fmt"

	"github.com/Veyal/interseptor/internal/msgcodec"
)

// CodecRequest is the request a BodyEncoder sees: the resolved plaintext body
// plus enough context for codecs that key off host/path/headers.
type CodecRequest struct {
	Method  string
	Scheme  string
	Host    string
	Port    int
	Path    string
	Headers map[string][]string
	Body    string // plaintext
}

// BodyEncoder turns a plaintext request body into its wire form (the encoded
// formSubmission style flows). id selects a codec explicitly; "" means "apply
// any apply_on_send codec that matches". applied is the codec id used, or ""
// when none matched.
type BodyEncoder interface {
	EncodeRequest(id string, req CodecRequest) (body, applied string, err error)
}

// MsgcodecEncoder adapts the existing Starlark message codecs.
type MsgcodecEncoder struct{ Codecs []*msgcodec.Codec }

// EncodeRequest implements BodyEncoder. With an explicit id the codec's
// encode() is applied directly (the item body IS the plaintext, so its
// match() — written against wire bodies — is not consulted). In auto mode only
// enabled apply_on_send codecs whose match() accepts the request apply.
func (e MsgcodecEncoder) EncodeRequest(id string, req CodecRequest) (string, string, error) {
	flow := msgcodec.Flow{
		Method: req.Method, Scheme: req.Scheme, Host: req.Host, Port: req.Port,
		Path: req.Path, ReqHeaders: req.Headers, ReqBody: req.Body,
	}
	for _, c := range e.Codecs {
		if id != "" {
			if c.Meta.ID != id {
				continue
			}
			out, err := c.Encode(flow, "req", req.Body)
			return out, id, err
		}
		if !c.Meta.ApplyOnSend || !c.Meta.Enabled {
			continue
		}
		ok, err := c.Match(flow, "req")
		if err != nil {
			return "", "", fmt.Errorf("codec %s match: %w", c.Meta.ID, err)
		}
		if !ok {
			continue
		}
		out, err := c.Encode(flow, "req", req.Body)
		return out, c.Meta.ID, err
	}
	if id != "" {
		return "", "", fmt.Errorf("codec %q not found", id)
	}
	return req.Body, "", nil
}
