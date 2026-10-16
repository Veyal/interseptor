package soap

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Namespaces the importer recognises.
const (
	nsWSDL11     = "http://schemas.xmlsoap.org/wsdl/"
	nsWSDL20     = "http://www.w3.org/ns/wsdl"
	nsWSDL20SOAP = "http://www.w3.org/ns/wsdl/soap"
	nsWSDL20RPC  = "http://www.w3.org/ns/wsdl/rpc"
	nsSoap11Bind = "http://schemas.xmlsoap.org/wsdl/soap/"
	nsSoap12Bind = "http://schemas.xmlsoap.org/wsdl/soap12/"
	nsSoap11Env  = "http://schemas.xmlsoap.org/soap/envelope/"
	nsSoap12Env  = "http://www.w3.org/2003/05/soap-envelope"
	nsSoapEnc    = "http://schemas.xmlsoap.org/soap/encoding/"
	nsXSD        = "http://www.w3.org/2001/XMLSchema"
	nsXSI        = "http://www.w3.org/2001/XMLSchema-instance"
	nsXML        = "http://www.w3.org/XML/1998/namespace"
	nsSoapUI     = "http://eviware.com/soapui/config"
	nsPolicy2004 = "http://schemas.xmlsoap.org/ws/2004/09/policy"
	nsPolicy2007 = "http://www.w3.org/ns/ws-policy"
	nsSP2005     = "http://schemas.xmlsoap.org/ws/2005/07/securitypolicy"
	nsSP2007     = "http://docs.oasis-open.org/ws-sx/ws-securitypolicy/200702"
	soapHTTPTr   = "http://schemas.xmlsoap.org/soap/http"
)

// Limits on the XML parser itself. They bound CPU and memory for hostile
// input; the entity/DTD refusal in parseXML removes XXE and entity-expansion
// attacks outright.
const (
	maxXMLDepth = 200
	maxXMLNodes = 600000
)

// budget is shared by every XML parse of one import (a SoapUI project embeds
// further documents), so nesting cannot multiply the work.
type budget struct{ nodes int }

type attr struct{ space, local, val string }

// node is a minimal, namespace-aware element tree.
type node struct {
	space, local string
	attrs        []attr
	kids         []*node
	text         string
	parent       *node
	ns           map[string]string // prefix -> uri declared on this element
}

// parseXML builds a tree. It refuses every DTD/entity declaration (any
// xml.Directive token), so external entities are never resolved and nothing
// can be expanded; undeclared entities fail in strict mode. It never fetches
// or reads anything.
func parseXML(data []byte, b *budget) (*node, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	dec.Entity = nil
	var root, cur *node
	var text strings.Builder
	flush := func() {
		if cur != nil && text.Len() > 0 {
			cur.text += text.String()
		}
		text.Reset()
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			return nil, fmt.Errorf("%w: %s", ErrDTD, clipStr(string(t), 40))
		case xml.StartElement:
			flush()
			if cur == nil && root != nil {
				return nil, fmt.Errorf("%w: multiple root elements", ErrMalformed)
			}
			b.nodes++
			if b.nodes > maxXMLNodes {
				return nil, ErrTooComplex
			}
			n := &node{space: t.Name.Space, local: t.Name.Local, parent: cur}
			for _, a := range t.Attr {
				switch {
				case a.Name.Space == "xmlns":
					if n.ns == nil {
						n.ns = map[string]string{}
					}
					n.ns[a.Name.Local] = a.Value
				case a.Name.Space == "" && a.Name.Local == "xmlns":
					if n.ns == nil {
						n.ns = map[string]string{}
					}
					n.ns[""] = a.Value
				default:
					n.attrs = append(n.attrs, attr{a.Name.Space, a.Name.Local, a.Value})
				}
			}
			if cur != nil {
				cur.kids = append(cur.kids, n)
			} else {
				root = n
			}
			cur = n
			if depthOf(n) > maxXMLDepth {
				return nil, ErrTooComplex
			}
		case xml.EndElement:
			flush()
			if cur == nil {
				return nil, fmt.Errorf("%w: unbalanced end tag", ErrMalformed)
			}
			cur = cur.parent
		case xml.CharData:
			if cur == nil {
				if len(bytes.TrimSpace(t)) > 0 {
					return nil, fmt.Errorf("%w: text outside the root element", ErrMalformed)
				}
				continue
			}
			text.Write(t)
		}
	}
	if root == nil {
		return nil, fmt.Errorf("%w: no root element", ErrMalformed)
	}
	if cur != nil {
		return nil, fmt.Errorf("%w: unexpected end of input", ErrMalformed)
	}
	return root, nil
}

func depthOf(n *node) int {
	d := 0
	for p := n; p != nil; p = p.parent {
		d++
	}
	return d
}

func clipStr(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// resolve maps a prefix to its URI. With elem, an empty prefix means the
// default namespace; the "xml" prefix is predefined.
func (n *node) resolve(prefix string, elem bool) string {
	if prefix == "xml" {
		return nsXML
	}
	if prefix == "" && !elem {
		return ""
	}
	for p := n; p != nil; p = p.parent {
		if uri, ok := p.ns[prefix]; ok {
			return uri
		}
	}
	if prefix == "" {
		return ""
	}
	return prefix // undeclared prefix: keep it visible rather than guessing
}

// qname resolves a QName-typed attribute value ("p:local" or "local", the
// latter in the default namespace).
func (n *node) qname(v string) qn {
	v = strings.TrimSpace(v)
	if i := strings.IndexByte(v, ':'); i >= 0 {
		return qn{n.resolve(v[:i], false), v[i+1:]}
	}
	return qn{n.resolve("", true), v}
}

func (n *node) attr(local string) string {
	for _, a := range n.attrs {
		if a.space == "" && a.local == local {
			return a.val
		}
	}
	return ""
}

func (n *node) hasAttr(local string) bool {
	for _, a := range n.attrs {
		if a.space == "" && a.local == local {
			return true
		}
	}
	return false
}

func (n *node) attrNS(space, local string) (string, bool) {
	for _, a := range n.attrs {
		if a.space == space && a.local == local {
			return a.val, true
		}
	}
	return "", false
}

// is reports whether the element is {space}local.
func (n *node) is(space, local string) bool { return n.space == space && n.local == local }

func (n *node) child(space, local string) *node {
	for _, k := range n.kids {
		if k.is(space, local) {
			return k
		}
	}
	return nil
}

func (n *node) childrenNamed(space, local string) []*node {
	var out []*node
	for _, k := range n.kids {
		if k.is(space, local) {
			out = append(out, k)
		}
	}
	return out
}

// childLocal finds a child by local name in any namespace.
func (n *node) childLocal(local string) *node {
	for _, k := range n.kids {
		if k.local == local {
			return k
		}
	}
	return nil
}

func (n *node) walk(fn func(*node)) {
	fn(n)
	for _, k := range n.kids {
		k.walk(fn)
	}
}
