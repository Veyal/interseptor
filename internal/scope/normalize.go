package scope

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// NormalizeRule turns what a person typically pastes into the Host field into
// a rule that can actually match a captured flow. It trims the host; parses a
// pasted URL into scheme/host/port/path; splits host:port into the port field;
// lowercases plain and wildcard hosts; and rejects hosts that can never match
// (spaces or other characters no hostname contains). Regex hosts are left as
// typed apart from trimming. Explicit scheme/port/path fields are never
// overridden: a conflicting port is an error.
func NormalizeRule(r *store.ScopeRule) error {
	r.Host = strings.TrimSpace(r.Host)
	r.Path = strings.TrimSpace(r.Path)
	r.Scheme = strings.ToLower(strings.TrimSpace(r.Scheme))
	host := r.Host
	if host == "" {
		return nil
	}
	if strings.Contains(host, "://") {
		u, err := url.Parse(host)
		if err != nil || u.Hostname() == "" {
			return fmt.Errorf("host %q is not a valid URL; enter a hostname such as example.com", host)
		}
		scheme := strings.ToLower(u.Scheme)
		if scheme != "http" && scheme != "https" {
			return fmt.Errorf("scheme must be http or https (got %q)", u.Scheme)
		}
		if r.Scheme == "" {
			r.Scheme = scheme
		}
		if p := u.Port(); p != "" {
			if err := setHostPort(r, p); err != nil {
				return err
			}
		}
		if p := u.EscapedPath(); p != "" && p != "/" && r.Path == "" {
			r.Path = p
		}
		host = u.Hostname()
	} else if !looksLikeRegex(host) {
		host = strings.TrimSuffix(host, "/")
		if strings.HasPrefix(host, "[") {
			end := strings.Index(host, "]")
			if end < 0 {
				return fmt.Errorf("host %q has an unclosed [", host)
			}
			rest := host[end+1:]
			host = host[1:end]
			if rest != "" {
				if !strings.HasPrefix(rest, ":") {
					return fmt.Errorf("host %q is not a valid hostname", r.Host)
				}
				if err := setHostPort(r, rest[1:]); err != nil {
					return err
				}
			}
		} else if strings.Count(host, ":") == 1 {
			i := strings.Index(host, ":")
			if err := setHostPort(r, host[i+1:]); err != nil {
				return err
			}
			host = host[:i]
		}
	}
	if !looksLikeRegex(host) {
		host = strings.ToLower(host)
		if err := checkPlainHost(host); err != nil {
			return err
		}
	}
	r.Host = host
	return nil
}

func setHostPort(r *store.ScopeRule, raw string) error {
	p, err := strconv.Atoi(raw)
	if err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("port %q must be a number between 1 and 65535", raw)
	}
	if r.Port != 0 && r.Port != p {
		return fmt.Errorf("host carries port %d but the port field is %d; use one", p, r.Port)
	}
	r.Port = p
	return nil
}

func checkPlainHost(h string) error {
	if h == "" {
		return fmt.Errorf("host is empty; enter a hostname such as example.com")
	}
	for _, c := range h {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '-', c == '_', c == ':', c == '*', c > 127:
		default:
			return fmt.Errorf("host %q contains %q, which no hostname has; use a bare host such as example.com, *.example.com, or a regex like .*example.*", h, string(c))
		}
	}
	return nil
}
