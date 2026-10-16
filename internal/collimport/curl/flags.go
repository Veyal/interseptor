package curl

// flagClass says how an option is treated.
type flagClass int

const (
	fSilent  flagClass = iota // accepted, no effect on the request, no report
	fIgnored                  // accepted, no effect in a collection request; reported degraded
	fUnsupp                   // changes request semantics we cannot represent; reported unsupported
	fHandled                  // consumed by the request builder
)

type flagSpec struct {
	arg   bool
	class flagClass
	name  string // canonical long name (without dashes)
}

// shortFlags maps -x to its canonical long name.
var shortFlags = map[byte]string{
	'A': "user-agent", 'b': "cookie", 'c': "cookie-jar", 'C': "continue-at", 'd': "data", 'D': "dump-header",
	'e': "referer", 'E': "cert", 'F': "form", 'G': "get", 'H': "header", 'I': "head", 'i': "include",
	'k': "insecure", 'K': "config", 'L': "location", 'm': "max-time", 'o': "output", 'O': "remote-name",
	'r': "range", 's': "silent", 'S': "show-error", 'T': "upload-file", 'u': "user", 'U': "proxy-user",
	'v': "verbose", 'w': "write-out", 'x': "proxy", 'X': "request", 'f': "fail", 'g': "globoff",
	'j': "junk-session-cookies", 'J': "remote-header-name", 'N': "no-buffer", 'n': "netrc", 'p': "proxytunnel",
	'q': "disable", 'R': "remote-time", 'Z': "parallel", 'Y': "speed-limit", 'y': "speed-time", 'z': "time-cond",
	'0': "http1.0", '1': "tlsv1", '2': "sslv2", '3': "sslv3", '4': "ipv4", '6': "ipv6", 'a': "append",
	'B': "use-ascii", 'l': "list-only", 'M': "manual", 'V': "version", 't': "telnet-option", 'Q': "quote", 'P': "ftp-port",
}

var longFlags = map[string]flagSpec{
	"request": {true, fHandled, "request"}, "header": {true, fHandled, "header"}, "data": {true, fHandled, "data"},
	"data-raw": {true, fHandled, "data-raw"}, "data-binary": {true, fHandled, "data-binary"},
	"data-ascii": {true, fHandled, "data-ascii"}, "data-urlencode": {true, fHandled, "data-urlencode"},
	"json": {true, fHandled, "json"}, "form": {true, fHandled, "form"}, "form-string": {true, fHandled, "form-string"},
	"user": {true, fHandled, "user"}, "cookie": {true, fHandled, "cookie"}, "get": {false, fHandled, "get"},
	"head": {false, fHandled, "head"}, "insecure": {false, fHandled, "insecure"}, "location": {false, fHandled, "location"},
	"location-trusted": {false, fHandled, "location"}, "compressed": {false, fHandled, "compressed"},
	"user-agent": {true, fHandled, "user-agent"}, "referer": {true, fHandled, "referer"}, "range": {true, fHandled, "range"},
	"url": {true, fHandled, "url"}, "max-time": {true, fHandled, "max-time"}, "max-redirs": {true, fHandled, "max-redirs"},
	"upload-file": {true, fHandled, "upload-file"}, "oauth2-bearer": {true, fHandled, "oauth2-bearer"},
	"digest": {false, fHandled, "digest"}, "ntlm": {false, fHandled, "ntlm"}, "basic": {false, fHandled, "basic"},
	"anyauth": {false, fIgnored, "anyauth"}, "negotiate": {false, fIgnored, "negotiate"},
	"cookie-jar": {true, fIgnored, "cookie-jar"}, "output": {true, fIgnored, "output"}, "remote-name": {false, fIgnored, "remote-name"},
	"dump-header": {true, fIgnored, "dump-header"}, "write-out": {true, fIgnored, "write-out"}, "config": {true, fIgnored, "config"},
	"connect-timeout": {true, fIgnored, "connect-timeout"}, "retry": {true, fIgnored, "retry"}, "retry-delay": {true, fIgnored, "retry-delay"},
	"retry-max-time": {true, fIgnored, "retry-max-time"}, "limit-rate": {true, fIgnored, "limit-rate"}, "parallel": {false, fIgnored, "parallel"},
	"continue-at": {true, fIgnored, "continue-at"}, "netrc": {false, fIgnored, "netrc"}, "fail": {false, fIgnored, "fail"},
	"fail-with-body": {false, fIgnored, "fail-with-body"}, "path-as-is": {false, fIgnored, "path-as-is"}, "tcp-nodelay": {false, fIgnored, "tcp-nodelay"},
	"keepalive-time": {true, fIgnored, "keepalive-time"}, "speed-limit": {true, fIgnored, "speed-limit"}, "speed-time": {true, fIgnored, "speed-time"},
	"tr-encoding": {false, fIgnored, "tr-encoding"}, "http1.0": {false, fIgnored, "http1.0"}, "http1.1": {false, fIgnored, "http1.1"},
	"http2": {false, fIgnored, "http2"}, "http2-prior-knowledge": {false, fIgnored, "http2-prior-knowledge"}, "http3": {false, fIgnored, "http3"},
	"tlsv1": {false, fIgnored, "tlsv1"}, "tlsv1.0": {false, fIgnored, "tlsv1.0"}, "tlsv1.1": {false, fIgnored, "tlsv1.1"},
	"tlsv1.2": {false, fIgnored, "tlsv1.2"}, "tlsv1.3": {false, fIgnored, "tlsv1.3"}, "tls-max": {true, fIgnored, "tls-max"},
	"ipv4": {false, fIgnored, "ipv4"}, "ipv6": {false, fIgnored, "ipv6"}, "proxy-user": {true, fUnsupp, "proxy-user"},
	"proxy": {true, fUnsupp, "proxy"}, "socks5": {true, fUnsupp, "socks5"}, "socks5-hostname": {true, fUnsupp, "socks5-hostname"},
	"proxytunnel": {false, fIgnored, "proxytunnel"}, "noproxy": {true, fIgnored, "noproxy"},
	"cert": {true, fUnsupp, "cert"}, "key": {true, fUnsupp, "key"}, "cacert": {true, fUnsupp, "cacert"}, "capath": {true, fUnsupp, "capath"},
	"cert-type": {true, fIgnored, "cert-type"}, "key-type": {true, fIgnored, "key-type"}, "pass": {true, fUnsupp, "pass"},
	"resolve": {true, fUnsupp, "resolve"}, "connect-to": {true, fUnsupp, "connect-to"}, "interface": {true, fUnsupp, "interface"},
	"unix-socket": {true, fUnsupp, "unix-socket"}, "aws-sigv4": {true, fUnsupp, "aws-sigv4"}, "doh-url": {true, fIgnored, "doh-url"},
	"ssl-no-revoke": {false, fIgnored, "ssl-no-revoke"}, "ssl": {false, fIgnored, "ssl"}, "ciphers": {true, fIgnored, "ciphers"},
	"include": {false, fSilent, "include"}, "silent": {false, fSilent, "silent"}, "show-error": {false, fSilent, "show-error"},
	"verbose": {false, fSilent, "verbose"}, "progress-bar": {false, fSilent, "progress-bar"}, "no-progress-meter": {false, fSilent, "no-progress-meter"},
	"globoff": {false, fSilent, "globoff"}, "no-buffer": {false, fSilent, "no-buffer"}, "disable": {false, fSilent, "disable"},
	"remote-time": {false, fSilent, "remote-time"}, "styled-output": {false, fSilent, "styled-output"}, "trace": {true, fSilent, "trace"},
	"trace-ascii": {true, fSilent, "trace-ascii"}, "trace-time": {false, fSilent, "trace-time"}, "stderr": {true, fSilent, "stderr"},
	"no-keepalive": {false, fSilent, "no-keepalive"}, "raw": {false, fIgnored, "raw"}, "compressed-ssh": {false, fSilent, "compressed-ssh"},
	"fail-early": {false, fSilent, "fail-early"}, "remote-header-name": {false, fSilent, "remote-header-name"},
	"junk-session-cookies": {false, fSilent, "junk-session-cookies"}, "http0.9": {false, fIgnored, "http0.9"},
}

// shortWithArg lists short options whose value follows (attached or next word).
var shortWithArg = map[byte]bool{
	'A': true, 'b': true, 'c': true, 'C': true, 'd': true, 'D': true, 'e': true, 'E': true, 'F': true, 'H': true,
	'K': true, 'm': true, 'o': true, 'r': true, 'T': true, 'u': true, 'U': true, 'w': true, 'x': true, 'X': true,
	'Y': true, 'y': true, 'z': true, 't': true, 'Q': true, 'P': true,
}
