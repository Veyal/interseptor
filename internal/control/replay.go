package control

import (
	"fmt"
	"html"
	"net/http"
	"strconv"

	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

const maxReplayRequestBytes = 4 << 10

type replayJSON struct {
	Session string `json:"session"` // "current" | "flow" (default: flow)
}

// replayFlow re-sends a captured flow's request, creating a fresh Repeater flow.
//   - session="flow" (default): replay exactly as captured — the flow's own
//     headers, no current-session override (NoSession).
//   - session="current": let the configured session headers override the
//     captured ones, so the replay runs under the active auth context.
func (h *toolsAPI) replayFlow(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad flow id")
		return
	}
	var in replayJSON
	if !decodeOptionalLimitedJSON(w, r, maxReplayRequestBytes, &in) {
		return
	}
	if in.Session != "" && in.Session != "flow" && in.Session != "current" {
		httpErr(w, http.StatusBadRequest, "session must be flow or current")
		return
	}
	useCurrent := in.Session == "current"

	f, err := h.st.GetFlow(id)
	if err != nil {
		httpNotFoundOrInternal(w, err, "flow not found")
		return
	}
	if f == nil {
		httpInternalErr(w, fmt.Errorf("flow %d lookup returned nil without error", id))
		return
	}
	if f.Method == "" {
		httpErr(w, http.StatusBadRequest, "flow has no request to replay")
		return
	}
	url := flowURLStr(f)
	if h.targetsOwnListener(url) {
		httpErr(w, http.StatusForbidden, "refusing to send to Interseptor's own listener")
		return
	}
	flow, err := h.snd.Send(sender.Request{
		Method:    f.Method,
		URL:       url,
		Headers:   f.ReqHeaders,
		Body:      h.bodyBytes(f.ReqBodyHash),
		Flags:     store.FlagRepeater | aiSourceFlag(r),
		NoSession: !useCurrent,
	})
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.flowDetail(flow))
}

// replayPage serves the side-effect-free confirmation page a replay link opens.
// It only previews the request; the actual send happens when the operator clicks
// "Replay now", which POSTs to /api/flows/{id}/replay (same-origin, CSRF-guarded).
func (h *toolsAPI) replayPage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad flow id", http.StatusBadRequest)
		return
	}
	f, err := h.st.GetFlow(id)
	if err != nil {
		httpTextNotFoundOrInternal(w, err, "flow not found")
		return
	}
	if f == nil {
		httpTextNotFoundOrInternal(w, fmt.Errorf("flow %d lookup returned nil without error", id), "flow not found")
		return
	}
	session := "flow"
	if r.URL.Query().Get("session") == "current" {
		session = "current"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(replayPageHTML(id, f.Method, flowURLStr(f), f.ReqLen, session)))
}

func replayPageHTML(id int64, method, url string, bodyLen int64, session string) string {
	sel := func(mode string) string {
		if mode == session {
			return " active"
		}
		return ""
	}
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Replay flow #%[1]d · Interseptor</title>
<style>
:root{--bg:#0e1014;--panel:#171a21;--line:#262a33;--fg:#e7e9ee;--fg2:#9aa3b2;--accent:#4ea1ff;--ok:#38c172;--err:#ff6b6b}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:var(--bg);color:var(--fg);font:14px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif;padding:24px}
.card{width:min(560px,100%%);background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:22px 24px;box-shadow:0 20px 60px rgba(0,0,0,.5)}
h1{margin:0 0 4px;font-size:15px;letter-spacing:.3px}
.sub{color:var(--fg2);font-size:12px;margin-bottom:18px}
.req{display:flex;align-items:baseline;gap:8px;background:#0f1218;border:1px solid var(--line);border-radius:8px;padding:10px 12px;margin-bottom:16px;overflow-x:auto}
.method{font-weight:700;color:var(--accent);flex:none}
.url{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12.5px;word-break:break-all}
.meta{color:var(--fg2);font-size:12px;margin-bottom:16px}
.lbl{font-size:10px;font-weight:700;letter-spacing:.8px;color:var(--fg2);text-transform:uppercase;margin-bottom:7px}
.seg{display:inline-flex;gap:2px;background:#0f1218;border:1px solid var(--line);border-radius:8px;padding:3px;margin-bottom:20px}
.seg button{background:transparent;border:none;color:var(--fg2);font:inherit;font-size:12.5px;font-weight:600;padding:6px 12px;border-radius:6px;cursor:pointer}
.seg button.active{background:var(--accent);color:#08131f}
.go{width:100%%;background:var(--accent);color:#08131f;border:none;border-radius:8px;padding:11px;font:inherit;font-weight:700;font-size:14px;cursor:pointer}
.go:disabled{opacity:.6;cursor:default}
.go:hover:not(:disabled){filter:brightness(1.08)}
.hint{color:var(--fg2);font-size:11.5px;margin-top:12px;text-align:center}
.res{margin-top:16px;padding:12px;border-radius:8px;border:1px solid var(--line);background:#0f1218;font-size:13px;display:none}
.res.show{display:block}
.res .st{font-weight:700}
.res a{color:var(--accent)}
</style></head>
<body>
<div class="card">
  <h1>Replay request</h1>
  <div class="sub">Flow #%[1]d — this only sends when you click below.</div>
  <div class="req"><span class="method">%[2]s</span><span class="url">%[3]s</span></div>
  <div class="meta">Request body: %[4]d bytes</div>
  <div class="lbl">Send under which session</div>
  <div class="seg" id="seg">
    <button type="button" data-s="current"%[5]s>Current session</button>
    <button type="button" data-s="flow"%[6]s>Flow's session</button>
  </div>
  <button class="go" id="go">Replay now</button>
  <div class="hint">The result appears here and as a new flow in History.</div>
  <div class="res" id="res"></div>
</div>
<script>
(function(){
  var session=%[7]q;
  var seg=document.getElementById('seg'), go=document.getElementById('go'), res=document.getElementById('res');
  seg.addEventListener('click',function(e){
    var b=e.target.closest('button[data-s]'); if(!b)return;
    session=b.dataset.s;
    [].forEach.call(seg.children,function(c){c.classList.toggle('active',c===b);});
  });
  go.addEventListener('click',function(){
    go.disabled=true; go.textContent='Replaying…'; res.className='res';
    fetch('/api/flows/%[1]d/replay',{method:'POST',headers:{'content-type':'application/json','X-Interseptor-CSRF':'1'},body:JSON.stringify({session:session})})
      .then(function(r){return r.json().then(function(d){return {ok:r.ok,d:d};});})
      .then(function(o){
        if(!o.ok){throw new Error((o.d&&o.d.error)||'send failed');}
        var d=o.d, st=d.status||d.error||'sent';
        res.innerHTML='<div class="st" style="color:var(--ok)">Sent — response '+String(st).replace(/[<>&]/g,'')+'</div><div style="margin-top:6px;color:var(--fg2)">New flow #'+d.id+' captured in History.</div>';
        res.classList.add('show');
      })
      .catch(function(err){
        res.innerHTML='<div class="st" style="color:var(--err)">Failed: '+String(err.message||err).replace(/[<>&]/g,'')+'</div>';
        res.classList.add('show');
      })
      .finally(function(){go.disabled=false; go.textContent='Replay again';});
  });
})();
</script>
</body></html>`,
		id, html.EscapeString(method), html.EscapeString(url), bodyLen, sel("current"), sel("flow"), session)
}
