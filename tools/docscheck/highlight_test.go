package main

import (
	"html"
	"regexp"
	"strings"
	"testing"
)

// sp is the expected markup for one highlighted token.
func sp(class, text string) string {
	return `<span class="tok-` + class + `">` + esc(text) + `</span>`
}

func esc(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(text)
}

func TestHighlightGoldens(t *testing.T) {
	tests := []struct {
		name, lang, code, want string
	}{
		{"bash comments strings flags numbers", "bash",
			"# list flows\ncurl -s -H 'Accept: application/json' \"https://app.example.com/api/flows?limit=50\" --max-time 10 # trailing\n",
			sp("comment", "# list flows") + "\ncurl " + sp("flag", "-s") + " " + sp("flag", "-H") + " " + sp("string", "'Accept: application/json'") + " " +
				sp("string", `"https://app.example.com/api/flows?limit=50"`) + " " + sp("flag", "--max-time") + " " + sp("number", "10") + " " + sp("comment", "# trailing") + "\n"},
		{"bash keywords continuation and words", "sh",
			"for host in a b; do\n  curl --url=https://app.example.com/x?a=1#top \\\n    -d @body.json | jq .\ndone\n",
			sp("keyword", "for") + " host in a b; " + sp("keyword", "do") + "\n  curl " + sp("flag", "--url") + "=https://app.example.com/x?a=1#top \\\n    " +
				sp("flag", "-d") + " @body.json | jq .\n" + sp("keyword", "done") + "\n"},
		{"bash urls and variables", "shell",
			"C=http://127.0.0.1:9966; curl -s \"$C/api/version\" https://app.example.com/ok 2.1.0\n",
			"C=http://127.0.0.1:9966; curl " + sp("flag", "-s") + " " + sp("string", `"$C/api/version"`) + " " + sp("url", "https://app.example.com/ok") + " 2.1.0\n"},
		{"bash multi-line single quotes", "bash",
			"curl -d '{\n  \"a\": 1\n}' https://app.example.com/\n",
			"curl " + sp("flag", "-d") + " " + sp("string", "'{\n  \"a\": 1\n}'") + " " + sp("url", "https://app.example.com/") + "\n"},
		{"bash heredoc body stays plain", "zsh",
			"cat <<'EOF' > out.txt\n# not a comment\n\"not a string\nEOF\necho done # ok\n",
			"cat &lt;&lt;'EOF' &gt; out.txt\n# not a comment\n&quot;not a string\nEOF\necho done " + sp("comment", "# ok") + "\n"},
		{"bash with prompts treats other lines as output", "bash",
			"$ curl -s https://app.example.com/\n{\"ok\":true}\n",
			sp("prompt", "$ ") + "curl " + sp("flag", "-s") + " " + sp("url", "https://app.example.com/") + "\n{&quot;ok&quot;:true}\n"},
		{"console prompts and output", "console",
			"$ interseptor version\ninterseptor v2.12.1\n> echo \"done\" \\\n  --flag\n# id -u\n",
			sp("prompt", "$ ") + "interseptor version\ninterseptor v2.12.1\n" + sp("prompt", "> ") + "echo " + sp("string", `"done"`) + " \\\n  " + sp("flag", "--flag") + "\n" +
				sp("prompt", "# ") + "id " + sp("flag", "-u") + "\n"},
		{"json", "json",
			"{\n  \"name\": \"example\",\n  \"count\": 2,\n  \"ratio\": -1.5e3,\n  \"ok\": true,\n  \"none\": null,\n  \"list\": [\"a\\\"b\", 3, 1.2.3, \"...\"] // note\n}\n",
			"{\n  " + sp("key", `"name"`) + ": " + sp("string", `"example"`) + ",\n  " + sp("key", `"count"`) + ": " + sp("number", "2") + ",\n  " +
				sp("key", `"ratio"`) + ": " + sp("number", "-1.5e3") + ",\n  " + sp("key", `"ok"`) + ": " + sp("keyword", "true") + ",\n  " +
				sp("key", `"none"`) + ": " + sp("keyword", "null") + ",\n  " + sp("key", `"list"`) + ": [" + sp("string", `"a\"b"`) + ", " + sp("number", "3") +
				", 1.2.3, " + sp("string", `"..."`) + "] " + sp("comment", "// note") + "\n}\n"},
		{"jsonc uses the json lexer", "jsonc",
			"{\"mcpServers\": {} /* local */}\n",
			"{" + sp("key", `"mcpServers"`) + ": {} " + sp("comment", "/* local */") + "}\n"},
		{"http request and response", "http",
			"POST /api/findings HTTP/1.1\nHost: app.example.com\nContent-Type: application/json\n\n{\"title\": \"IDOR\"}\n\nHTTP/1.1 201 Created\nLocation: https://app.example.com/api/findings/7\n",
			sp("method", "POST") + " " + sp("url", "/api/findings") + " HTTP/1.1\n" + sp("key", "Host") + ": app.example.com\n" + sp("key", "Content-Type") + ": application/json\n\n{" +
				sp("key", `"title"`) + ": " + sp("string", `"IDOR"`) + "}\n\nHTTP/1.1 " + sp("status", "201") + " Created\n" + sp("key", "Location") + ": https://app.example.com/api/findings/7\n"},
		{"http headers only and plain bodies", "http",
			"Proxy-Authorization: Basic dXNlcjpwYXNz\n\nplain body: not a header\n",
			sp("key", "Proxy-Authorization") + ": Basic dXNlcjpwYXNz\n\nplain body: not a header\n"},
		{"http unrecognised stays plain", "http",
			"this is not http: really\n",
			"this is not http: really\n"},
		{"yaml", "yaml",
			"# site navigation\n- title: Start here\n  id: start\n  weight: 2\n  draft: false\n  links:\n    - url: /features/ # card\n      name: 'All features'\n  notes: |\n    key: not a key\n    # not a comment\n  done: ~\n",
			sp("comment", "# site navigation") + "\n- " + sp("key", "title") + ": Start here\n  " + sp("key", "id") + ": start\n  " + sp("key", "weight") + ": " + sp("number", "2") + "\n  " +
				sp("key", "draft") + ": " + sp("keyword", "false") + "\n  " + sp("key", "links") + ":\n    - " + sp("key", "url") + ": /features/ " + sp("comment", "# card") + "\n      " +
				sp("key", "name") + ": " + sp("string", "'All features'") + "\n  " + sp("key", "notes") + ": |\n    key: not a key\n    # not a comment\n  " + sp("key", "done") + ": " + sp("keyword", "~") + "\n"},
		{"yml alias and urls are not keys", "yml",
			"url: https://app.example.com/x\nhttp://app.example.com: no\n",
			sp("key", "url") + ": https://app.example.com/x\nhttp://app.example.com: no\n"},
		{"go", "go",
			"// Package main is an example.\npackage main\n\nimport \"fmt\"\n\nfunc main() {\n\tn := 0x1F + 42 // answer\n\ts := `raw\nline`\n\tfmt.Println(\"hi\\n\", n, s, 'x', nil, true, 1.5e3, format)\n}\n",
			sp("comment", "// Package main is an example.") + "\n" + sp("keyword", "package") + " main\n\n" + sp("keyword", "import") + " " + sp("string", `"fmt"`) + "\n\n" +
				sp("keyword", "func") + " main() {\n\tn := " + sp("number", "0x1F") + " + " + sp("number", "42") + " " + sp("comment", "// answer") + "\n\ts := " + sp("string", "`raw\nline`") +
				"\n\tfmt.Println(" + sp("string", `"hi\n"`) + ", n, s, " + sp("string", "'x'") + ", " + sp("keyword", "nil") + ", " + sp("keyword", "true") + ", " + sp("number", "1.5e3") + ", format)\n}\n"},
		{"javascript", "js",
			"// fetch a flow\nconst res = await fetch(`https://app.example.com/api/flows/${id}`, { method: \"GET\" });\nif (res.status === 404) return null;\nobj.default = 'x'; /* done */\n",
			sp("comment", "// fetch a flow") + "\n" + sp("keyword", "const") + " res = " + sp("keyword", "await") + " fetch(" + sp("string", "`https://app.example.com/api/flows/") + "${id}" + sp("string", "`") +
				", { method: " + sp("string", `"GET"`) + " });\n" + sp("keyword", "if") + " (res.status === " + sp("number", "404") + ") " + sp("keyword", "return") + " " + sp("keyword", "null") +
				";\nobj.default = " + sp("string", "'x'") + "; " + sp("comment", "/* done */") + "\n"},
		{"typescript", "typescript",
			"interface Flow { id: number; type?: string }\nexport type Kind = \"a\" | \"b\";\n",
			sp("keyword", "interface") + " Flow { id: number; type?: string }\n" + sp("keyword", "export") + " type Kind = " + sp("string", `"a"`) + " | " + sp("string", `"b"`) + ";\n"},
		{"python", "python",
			"# passive check\ndef check(flow):\n    if flow.status == 200 and \"token\" in flow.res_body:\n        return [finding(\"medium\", 'Token', evidence=r'\\d+')]\n    doc = \"\"\"multi\nline\"\"\"\n    return None\n",
			sp("comment", "# passive check") + "\n" + sp("keyword", "def") + " check(flow):\n    " + sp("keyword", "if") + " flow.status == " + sp("number", "200") + " " + sp("keyword", "and") + " " +
				sp("string", `"token"`) + " " + sp("keyword", "in") + " flow.res_body:\n        " + sp("keyword", "return") + " [finding(" + sp("string", `"medium"`) + ", " + sp("string", "'Token'") +
				", evidence=" + sp("string", `r'\d+'`) + ")]\n    doc = " + sp("string", "\"\"\"multi\nline\"\"\"") + "\n    " + sp("keyword", "return") + " " + sp("keyword", "None") + "\n"},
		{"starlark is python", "starlark",
			"def match(flow):\n    return flow.method == \"GET\" # only reads\n",
			sp("keyword", "def") + " match(flow):\n    " + sp("keyword", "return") + " flow.method == " + sp("string", `"GET"`) + " " + sp("comment", "# only reads") + "\n"},
		{"toml", "toml",
			"# rule pack\n[pack]\nname = \"example-pack\" # inline\nversion = '1.0.0'\nchecks = [\"a.star\", \"b.star\"]\nenabled = true\nretries = 3\nwhen = 1979-05-27\n[[servers]]\n\"quoted key\" = 0x1f\n",
			sp("comment", "# rule pack") + "\n" + sp("keyword", "[pack]") + "\n" + sp("key", "name") + " = " + sp("string", `"example-pack"`) + " " + sp("comment", "# inline") + "\n" +
				sp("key", "version") + " = " + sp("string", "'1.0.0'") + "\n" + sp("key", "checks") + " = [" + sp("string", `"a.star"`) + ", " + sp("string", `"b.star"`) + "]\n" +
				sp("key", "enabled") + " = " + sp("keyword", "true") + "\n" + sp("key", "retries") + " = " + sp("number", "3") + "\n" + sp("key", "when") + " = 1979-05-27\n" +
				sp("keyword", "[[servers]]") + "\n" + sp("key", `"quoted key"`) + " = " + sp("number", "0x1f") + "\n"},
		{"ini", "ini",
			"; settings\n[proxy]\nport = 8080\nhost = 127.0.0.1\n# hash comment\n",
			sp("comment", "; settings") + "\n" + sp("keyword", "[proxy]") + "\n" + sp("key", "port") + " = " + sp("number", "8080") + "\n" + sp("key", "host") + " = 127.0.0.1\n" + sp("comment", "# hash comment") + "\n"},
		{"unknown languages stay plain", "rust",
			"fn main() { println!(\"<b>&</b>\"); } // c\n",
			"fn main() { println!(&quot;&lt;b&gt;&amp;&lt;/b&gt;&quot;); } // c\n"},
		{"text stays plain", "text",
			"# not a comment \"x\" 42\n",
			"# not a comment &quot;x&quot; 42\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := highlightCode(tt.lang, tt.code); got != tt.want {
				t.Errorf("highlightCode(%q)\n got: %s\nwant: %s", tt.lang, got, tt.want)
			}
		})
	}
}

// visibleText is what highlighting must preserve: the code itself, except
// that NUL becomes U+FFFD as CommonMark requires for HTML output.
func visibleText(code string) string { return strings.ReplaceAll(code, "\x00", "\uFFFD") }

var (
	tokenSpan = regexp.MustCompile(`<span class="tok-(comment|string|number|keyword|key|prompt|flag|method|status|url)">([^<]*)</span>`)
	anyTag    = regexp.MustCompile(`[<>"]`)
)

// Every language must escape everything and wrap tokens in flat spans of the
// allowed classes, whatever the input: nothing in a code block can open an
// element, end the code element, or break out of an attribute.
func TestHighlightNeverBreaksOut(t *testing.T) {
	inputs := []string{
		`</span></code></pre><script>alert("x")</script>`,
		"\"unterminated string\n'it's'\n`unterminated ${ template",
		"&amp; &lt; <!-- --> ]]> <![CDATA[ x ]]>",
		"/* unterminated comment\n# \"quoted\" in a comment <b>\n// <i>",
		"\"\"\"unterminated triple\n'''also\n",
		"--flag=\"</code>\" -x '<img src=x onerror=alert(1)>' $ > # \\",
		"key: \"<b>\" # <tag>\n[section<]\nname = \"</span>\"\n",
		"GET /<script> HTTP/1.1\nX-<Bad>: <v>\n\n{\"<k>\": \"</v>\"}\n",
		"é ✓ 日本 \u00a0 \t\r\n\n",
		"",
		"\n\n",
		"0x 1e 1. .5 -- - 'a\\'b' \"a\\\"b\" `a\\`b`",
	}
	for _, lang := range []string{"bash", "sh", "shell", "zsh", "console", "json", "jsonc", "http", "yaml", "yml", "go", "js", "javascript", "ts", "typescript", "python", "py", "starlark", "toml", "ini", "text", "unknown"} {
		for _, in := range inputs {
			out := highlightCode(lang, in)
			text := tokenSpan.ReplaceAllString(out, "$2")
			if anyTag.MatchString(text) {
				t.Fatalf("%s: output has a raw <, > or \" outside the token spans:\n%s", lang, out)
			}
			if got := html.UnescapeString(text); got != visibleText(in) {
				t.Fatalf("%s: highlighting changed the code:\n in: %q\nout: %q", lang, in, got)
			}
		}
	}
}

// FuzzHighlight checks the escape and round-trip properties on arbitrary
// input; go test runs the seeds, go test -fuzz explores further.
func FuzzHighlight(f *testing.F) {
	for _, seed := range []string{"curl -s 'x' \"y\" # z", `{"a": [1, true]}`, "GET / HTTP/1.1\nHost: example.com\n\n{}", "a: 'b' # c\n- d: |\n  e", "x := `y` // z", "def f(): return r'x' # y", "[a]\nb = \"c\" # d", "`${a}` /* b */", "$ echo \\\n> x", "\x00"} {
		f.Add(seed)
	}
	langs := []string{"bash", "console", "json", "http", "yaml", "go", "js", "python", "toml", "ini"}
	f.Fuzz(func(t *testing.T, code string) {
		for _, lang := range langs {
			out := highlightCode(lang, code)
			text := tokenSpan.ReplaceAllString(out, "$2")
			if anyTag.MatchString(text) || html.UnescapeString(text) != visibleText(code) {
				t.Fatalf("%s: unsafe or lossy highlight of %q: %s", lang, code, out)
			}
		}
	})
}
