package collreport

import (
	"html/template"
	"io"
	"time"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
)

// writeHTML renders one self-contained page: inline CSS, no script, no
// external resource, light and dark themes. html/template escapes every field.
func writeHTML(w io.Writer, rep *collrun.Report) error {
	return htmlTpl.Execute(w, htmlView(rep))
}

type itemView struct {
	collrun.ItemResult
	Tag      string
	TagClass string
	Title    string
}

type pageView struct {
	*collrun.Report
	Started  string
	Duration string
	Exit     int
	Items    []itemView
}

func htmlView(rep *collrun.Report) pageView {
	v := pageView{Report: rep, Exit: rep.ExitCode()}
	if rep.StartedMs > 0 {
		v.Started = time.UnixMilli(rep.StartedMs).UTC().Format("2006-01-02 15:04:05 UTC")
	}
	if rep.FinishedMs > rep.StartedMs {
		v.Duration = (time.Duration(rep.FinishedMs-rep.StartedMs) * time.Millisecond).Round(time.Millisecond).String()
	}
	for _, it := range rep.Items {
		iv := itemView{ItemResult: it, Tag: "PASS", TagClass: "pass"}
		switch {
		case it.Outcome == collexec.OutcomeBlocked:
			iv.Tag, iv.TagClass = "BLOCKED", "block"
		case it.Outcome == collexec.OutcomeError:
			iv.Tag, iv.TagClass = "ERROR", "err"
		case it.Outcome == collexec.OutcomeSkipped:
			iv.Tag, iv.TagClass = "SKIPPED", "skip"
		case it.Problem():
			iv.Tag, iv.TagClass = "FAIL", "fail"
		}
		iv.Title = it.Name
		if it.Path != "" {
			iv.Title = it.Path + " / " + it.Name
		}
		v.Items = append(v.Items, iv)
	}
	return v
}

var htmlTpl = template.Must(template.New("report").Funcs(template.FuncMap{
	"inc": func(i int) int { return i + 1 },
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Run report: {{.CollectionName}}</title>
<style>
:root{color-scheme:light dark;--bg:#f6f7f9;--panel:#ffffff;--ink:#1b2430;--muted:#5b6675;--line:#d5dae1;--pass:#12663a;--fail:#a4261d;--err:#a4261d;--block:#8a5a00;--skip:#4a5565;--chip:#eef1f5}
@media (prefers-color-scheme:dark){:root{--bg:#10151c;--panel:#18202a;--ink:#e6ebf2;--muted:#9aa6b6;--line:#2b3644;--pass:#5fd592;--fail:#ff8a80;--err:#ff8a80;--block:#f5c15c;--skip:#9aa6b6;--chip:#202a36}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);font:16px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;padding:16px}
main{max-width:1000px;margin:0 auto}
h1{font-size:1.4rem;margin:0 0 4px}
.meta{color:var(--muted);margin:0 0 16px;overflow-wrap:anywhere}
.totals{display:grid;grid-template-columns:repeat(auto-fit,minmax(130px,1fr));gap:8px;margin:0 0 16px}
.totals div{background:var(--panel);border:1px solid var(--line);border-radius:6px;padding:8px 12px}
.totals b{display:block;font-size:1.3rem}
.totals span{color:var(--muted);font-size:.85rem}
details{background:var(--panel);border:1px solid var(--line);border-radius:6px;margin:0 0 8px}
summary{cursor:pointer;padding:8px 12px;display:flex;flex-wrap:wrap;gap:8px;align-items:baseline}
summary:focus-visible{outline:2px solid var(--ink);outline-offset:2px}
.tag{font-weight:700;font-size:.8rem;border:1px solid currentColor;border-radius:4px;padding:0 6px}
.pass{color:var(--pass)}.fail,.err{color:var(--fail)}.block{color:var(--block)}.skip{color:var(--skip)}
.title{font-weight:600;overflow-wrap:anywhere}
.sub{color:var(--muted);font-size:.9rem}
.body{padding:0 12px 12px}
table{border-collapse:collapse;width:100%;display:block;overflow-x:auto}
th,td{text-align:left;border-bottom:1px solid var(--line);padding:4px 8px;vertical-align:top}
pre{background:var(--chip);border-radius:4px;padding:8px;overflow-x:auto;white-space:pre-wrap;overflow-wrap:anywhere;margin:4px 0}
.note{border-left:4px solid var(--block);padding:4px 12px;margin:0 0 16px;background:var(--panel)}
@media (prefers-reduced-motion:no-preference){details{transition:none}}
</style>
</head>
<body>
<main>
<h1>{{.CollectionName}}</h1>
<p class="meta">{{with .EnvName}}Environment {{.}} &middot; {{end}}Run {{.RunUID}} &middot; {{.Source}} &middot; {{.Iterations}} iteration{{if ne .Iterations 1}}s{{end}}{{with .Started}} &middot; {{.}}{{end}}{{with .Duration}} &middot; {{.}}{{end}}</p>
<p class="meta"><strong>Status: {{.Status}}</strong>{{with .StopReason}} ({{.}}){{end}} &middot; exit code {{.Exit}}{{with .Data}} &middot; data {{.Rows}} rows ({{.Format}}){{end}}</p>
{{if .Quarantined}}<div class="note"><strong>Scripts not approved; nothing was sent.</strong><ul>{{range .Quarantined}}<li>{{.Owner}} {{.Name}} ({{.Listen}}): <code>{{.Hash}}</code></li>{{end}}</ul></div>{{end}}
<section class="totals" aria-label="Totals">
<div><b>{{.Totals.Requests}}</b><span>requests</span></div>
<div><b>{{.Totals.Pass}}</b><span>tests passed</span></div>
<div><b>{{.Totals.Fail}}</b><span>tests failed</span></div>
<div><b>{{.Totals.TestError}}</b><span>test errors</span></div>
<div><b>{{.Totals.Unsupported}}</b><span>unsupported</span></div>
<div><b>{{.Totals.Blocked}}</b><span>blocked</span></div>
<div><b>{{.Totals.Errors}}</b><span>request errors</span></div>
</section>
{{range .Items}}
<details{{if ne .TagClass "pass"}} open{{end}}>
<summary><span class="tag {{.TagClass}}">{{.Tag}}</span><span class="title">{{.Method}} {{.Title}}</span><span class="sub">{{if .HTTPStatus}}{{.HTTPStatus}} {{.StatusText}}{{end}}{{if .DurationMs}} &middot; {{.DurationMs}} ms{{end}}{{if .FlowID}} &middot; flow #{{.FlowID}}{{end}}{{if gt $.Iterations 1}} &middot; iteration {{inc .Iteration}}{{end}}</span></summary>
<div class="body">
{{with .URL}}<p class="sub">{{.}}</p>{{end}}
{{with .Error}}<pre>{{.}}</pre>{{end}}
{{if .Tests}}<table><thead><tr><th>Status</th><th>Test</th><th>Detail</th></tr></thead><tbody>
{{range .Tests}}<tr><td>{{.Status}}</td><td>{{.Name}}</td><td>{{.Message}}{{if or .Expected .Actual}} (expected {{.Expected}}, actual {{.Actual}}){{end}}</td></tr>{{end}}
</tbody></table>{{end}}
{{if .Console}}<pre>{{range .Console}}[{{.Level}}] {{.Text}}
{{end}}</pre>{{end}}
{{if .VarChanges}}<p class="sub">Variable writes: {{range .VarChanges}}{{.Scope}}.{{.Key}}{{if .Unset}} (unset){{else}} = {{.Display}}{{end}}; {{end}}</p>{{end}}
</div>
</details>
{{end}}
{{if .Truncated}}<p class="note">Rows were truncated at {{len .Items}} requests.</p>{{end}}
</main>
</body>
</html>
`))
