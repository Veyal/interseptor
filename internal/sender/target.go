package sender

import (
	"fmt"
	"net/url"
	"strings"
)

// TargetURL joins a connection target (scheme://host[:port]) with the request
// target of a raw request line and returns the URL to send to.
//
// The request target must be in origin form (start with "/"). A raw request
// line may carry any URI — "GET x:@other.example/a HTTP/1.1" parses to the
// path "@other.example/a" — and appending that to "http://target" moves the
// authority: the request would go to other.example with the real target as
// userinfo. Callers that vet the target (scope, own-listener) would never see
// the host that is actually dialled, so such a line is refused here.
func TargetURL(target, requestTarget string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(target))
	if err != nil || !base.IsAbs() || base.Host == "" {
		return "", fmt.Errorf("invalid target %q: want scheme://host[:port]", target)
	}
	if !strings.HasPrefix(requestTarget, "/") {
		return "", fmt.Errorf("request target %q must start with \"/\"", clipTarget(requestTarget))
	}
	joined := strings.TrimRight(strings.TrimSpace(target), "/") + requestTarget
	u, err := url.Parse(joined)
	if err != nil {
		return "", fmt.Errorf("invalid request target %q", clipTarget(requestTarget))
	}
	// Belt and braces: whatever the request target contained, the result must
	// still address the target's own authority.
	if !strings.EqualFold(u.Host, base.Host) || !strings.EqualFold(u.Scheme, base.Scheme) {
		return "", fmt.Errorf("request target %q does not stay on %s", clipTarget(requestTarget), base.Host)
	}
	return joined, nil
}

// clipTarget keeps an error message short when the request target is a payload.
func clipTarget(s string) string {
	const max = 80
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
