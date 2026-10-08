package collexec

import (
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxJarCookies = 3000

// JarCookie is one stored cookie with its matching metadata.
type JarCookie struct {
	Name     string
	Value    string
	Domain   string
	Path     string
	HostOnly bool
	Secure   bool
	HTTPOnly bool
	Expires  time.Time // zero = session
	created  int64
}

// Jar is an in-memory cookie jar scoped to one (collection, environment,
// identity) partition. Cookies never leave memory here; persistence into
// ix_cookies is a separate, scrubbed concern. Safe for concurrent use.
type Jar struct {
	mu      sync.Mutex
	cookies map[string]*JarCookie
	seq     int64
}

// NewJar returns an empty jar.
func NewJar() *Jar { return &Jar{cookies: map[string]*JarCookie{}} }

func jarKey(c *JarCookie) string { return c.Domain + "\x00" + c.Path + "\x00" + c.Name }

func defaultPath(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" || p[0] != '/' {
		return "/"
	}
	i := strings.LastIndexByte(p, '/')
	if i == 0 {
		return "/"
	}
	return p[:i]
}

func isIP(host string) bool { return net.ParseIP(host) != nil }

// Store records Set-Cookie cookies received from u at time now.
func (j *Jar) Store(u *url.URL, cs []*http.Cookie, now time.Time) {
	host := strings.ToLower(u.Hostname())
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, c := range cs {
		if c == nil || c.Name == "" {
			continue
		}
		jc := &JarCookie{Name: c.Name, Value: c.Value, Secure: c.Secure, HTTPOnly: c.HttpOnly}
		d := strings.ToLower(strings.TrimPrefix(c.Domain, "."))
		switch {
		case d == "" || isIP(host) && d != host:
			jc.Domain, jc.HostOnly = host, true
		case host == d || strings.HasSuffix(host, "."+d):
			jc.Domain = d
		default:
			continue // domain not covering the sender: reject
		}
		jc.Path = c.Path
		if jc.Path == "" || jc.Path[0] != '/' {
			jc.Path = defaultPath(u)
		}
		switch {
		case c.MaxAge < 0:
			delete(j.cookies, jarKey(jc))
			continue
		case c.MaxAge > 0:
			jc.Expires = now.Add(time.Duration(c.MaxAge) * time.Second)
		case !c.Expires.IsZero():
			jc.Expires = c.Expires
			if !c.Expires.After(now) {
				delete(j.cookies, jarKey(jc))
				continue
			}
		}
		k := jarKey(jc)
		if old, ok := j.cookies[k]; ok {
			jc.created = old.created
		} else {
			if len(j.cookies) >= maxJarCookies {
				continue
			}
			j.seq++
			jc.created = j.seq
		}
		j.cookies[k] = jc
	}
}

func pathMatch(reqPath, cookiePath string) bool {
	if reqPath == "" {
		reqPath = "/"
	}
	if reqPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(reqPath, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") || reqPath[len(cookiePath)] == '/'
}

func (c *JarCookie) matches(u *url.URL, now time.Time) bool {
	host := strings.ToLower(u.Hostname())
	if c.HostOnly {
		if host != c.Domain {
			return false
		}
	} else if host != c.Domain && !strings.HasSuffix(host, "."+c.Domain) {
		return false
	}
	if c.Secure && u.Scheme != "https" {
		return false
	}
	if !c.Expires.IsZero() && !c.Expires.After(now) {
		return false
	}
	return pathMatch(u.EscapedPath(), c.Path)
}

// Header returns the Cookie header value for u ("" when none match).
func (j *Jar) Header(u *url.URL, now time.Time) string {
	j.mu.Lock()
	var m []*JarCookie
	for _, c := range j.cookies {
		if c.matches(u, now) {
			m = append(m, c)
		}
	}
	j.mu.Unlock()
	sort.Slice(m, func(a, b int) bool {
		if len(m[a].Path) != len(m[b].Path) {
			return len(m[a].Path) > len(m[b].Path)
		}
		return m[a].created < m[b].created
	})
	parts := make([]string, len(m))
	for i, c := range m {
		parts[i] = c.Name + "=" + c.Value
	}
	return strings.Join(parts, "; ")
}

// List returns every unexpired cookie (for pm.cookies and the UI), sorted.
func (j *Jar) List(now time.Time) []JarCookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []JarCookie
	for _, c := range j.cookies {
		if c.Expires.IsZero() || c.Expires.After(now) {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		x, y := out[a], out[b]
		if x.Domain != y.Domain {
			return x.Domain < y.Domain
		}
		if x.Path != y.Path {
			return x.Path < y.Path
		}
		return x.Name < y.Name
	})
	return out
}

// Clear drops every cookie.
func (j *Jar) Clear() {
	j.mu.Lock()
	j.cookies = map[string]*JarCookie{}
	j.mu.Unlock()
}

// Export returns a copy of every stored cookie (session cookies included), for
// persistence or a restore point. Replace reverses it.
func (j *Jar) Export() []JarCookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]JarCookie, 0, len(j.cookies))
	for _, c := range j.cookies {
		out = append(out, *c)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].created < out[b].created })
	return out
}

// Replace swaps the jar's contents for cs.
func (j *Jar) Replace(cs []JarCookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.cookies = make(map[string]*JarCookie, len(cs))
	for i := range cs {
		c := cs[i]
		if c.Name == "" || c.Domain == "" {
			continue
		}
		if c.created == 0 {
			j.seq++
			c.created = j.seq
		} else if c.created > j.seq {
			j.seq = c.created
		}
		j.cookies[jarKey(&c)] = &c
	}
}

// Jars hands out one Jar per (collection, environment, identity) partition.
type Jars struct {
	// Loader, when set, supplies a partition's persisted cookies the first time
	// its jar is created.
	Loader func(collectionUID, envUID, identity string) []JarCookie

	mu sync.Mutex
	m  map[string]*Jar
}

// For returns the jar for a partition, creating it on first use.
func (js *Jars) For(collectionUID, envUID, identity string) *Jar {
	k := collectionUID + "\x00" + envUID + "\x00" + identity
	js.mu.Lock()
	defer js.mu.Unlock()
	if js.m == nil {
		js.m = map[string]*Jar{}
	}
	j, ok := js.m[k]
	if !ok {
		j = NewJar()
		if js.Loader != nil {
			j.Replace(js.Loader(collectionUID, envUID, identity))
		}
		js.m[k] = j
	}
	return j
}
