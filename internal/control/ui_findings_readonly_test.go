package control

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Findings are authored by agents over MCP/REST; the web UI is a reader unless
// findings.uiEditing is on. These source-level contracts pin the gate.
func TestFindingsReadOnlyGate(t *testing.T) {
	js := readUIAsset(t, "js/findings.js")
	css := readUIAsset(t, "findings.css")

	// One chokepoint: core.js defines the helper; findings.js imports it and
	// never keeps a competing definition or reads the raw setting itself.
	core := readUIAsset(t, "js/core.js")
	if n := strings.Count(core, "export function findingsEditable()"); n != 1 {
		t.Fatalf("core.js must define exactly one findingsEditable helper, got %d", n)
	}
	if n := strings.Count(js, "function findingsEditable()"); n != 0 {
		t.Fatalf("findings.js must import findingsEditable, not define it (got %d definitions)", n)
	}
	if n := strings.Count(js, "state.findingsUIEditing"); n != 0 {
		t.Fatalf("state.findingsUIEditing must be read only by the core.js helper, got %d reads in findings.js", n)
	}
	for _, name := range []string{"js/findings.js", "js/evidence-attach.js", "js/finding-revisions.js"} {
		src := readUIAsset(t, name)
		if name == "js/evidence-attach.js" {
			continue // loads core lazily, reaches the helper through the core namespace
		}
		if !regexp.MustCompile(`import\s*\{[^}]*\bfindingsEditable\b[^}]*\}\s*from\s*'./core.js'`).MatchString(src) {
			t.Errorf("%s must import findingsEditable from core.js", name)
		}
	}
	for _, want := range []string{
		"if(!findingsEditable())findEditMode=false;",
		"if(findingsEditable()&&(findingDrafts.has(selFinding)||cvssPreviewDrafts.has(selFinding)))findEditMode=true;",
		"if(!findingsEditable())return;",
		"const edit = findEditMode && findingsEditable();",
		"findingsEditable() ? { label: 'Delete', icon: 'trash', danger: true",
		"async function confirmDeleteFinding(f) {\n  if (!findingsEditable()) return;",
		"findingsEditable() ? `<button class=\"btn ${edit ? '' : 'btn-primary'}\" id=\"findToggleEdit\"",
		"syncFindingsEditChrome",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("findings.js missing gate anchor %q", want)
		}
	}
	// Every assignment of true must be guarded.
	for _, m := range regexp.MustCompile(`findEditMode\s*=\s*true`).FindAllStringIndex(js, -1) {
		start := m[0] - 160
		if start < 0 {
			start = 0
		}
		if !strings.Contains(js[start:m[1]], "findingsEditable()") {
			t.Errorf("unguarded findEditMode=true near %q", js[start:m[1]])
		}
	}

	// Mode chip.
	for _, want := range []string{
		`id="findModeChip"`, `<button type="button" class="btn xs find-mode-chip"`,
		`href="#i-lock"`, `href="#i-lock-open"`, "Agent-maintained", "Editing on",
		"aria-label=", "openSettingsSection('project')",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("findings.js missing mode chip anchor %q", want)
		}
	}
	chip := js[strings.Index(js, `id="findModeChip"`):]
	chip = chip[:strings.Index(chip, "</button>")]
	if !strings.Contains(chip, "aria-label=") {
		t.Error("mode chip needs an aria-label")
	}

	// Live re-render, deferred when a draft or save is in flight.
	if !strings.Contains(js, "window.addEventListener('interseptor:findings-ui-editing'") {
		t.Fatal("findings.js must listen for interseptor:findings-ui-editing")
	}
	l := js[strings.Index(js, "window.addEventListener('interseptor:findings-ui-editing'"):]
	l = l[:strings.Index(l, "\n});")]
	for _, want := range []string{"findingDetailEditPending()", "findingDetailRefreshDeferred = true", "findingDrafts.hasAny()"} {
		if !strings.Contains(l, want) {
			t.Errorf("editing listener must defer safely, missing %q", want)
		}
	}

	// Colours come from classes, not inline styles.
	if strings.Contains(js, `style="color:`) {
		t.Error("findings.js must not use inline style=\"color:")
	}
	for _, c := range []string{".sev-critical", ".sev-high", ".sev-medium", ".sev-low", ".sev-info",
		".find-st-verified", ".find-st-fixed", ".find-st-needs_verification", ".find-st-false_positive", ".find-st-wont_fix",
		".find-gate-ok.is-pass", ".find-gate-ok.is-fail", ".find-mode-chip"} {
		if !strings.Contains(css, c) {
			t.Errorf("findings.css missing %s", c)
		}
	}
	if !strings.Contains(js[strings.Index(js, "function findingRowKey"):strings.Index(js, "function renderFindings()")], "findingsEditable()") {
		t.Error("row key must include the editing mode")
	}
}

const findingsOffMessage = "Findings editing is off. Enable it in Settings."

func TestFindingsOffMessageIsOneString(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	if n := strings.Count(core, findingsOffMessage); n != 1 {
		t.Fatalf("the off-state toast must be defined once in core.js, got %d", n)
	}
	entries, err := filepath.Glob("ui/js/*.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Base(e) == "core.js" {
			continue
		}
		b, _ := os.ReadFile(e)
		if strings.Contains(string(b), findingsOffMessage) {
			t.Errorf("%s hard-codes the off-state message; use core.js", e)
		}
	}
}

// nodeModule runs script as an ES module and fails with its output.
func nodeModule(t *testing.T, script string) {
	t.Helper()
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func sliceBetween(t *testing.T, src, from, to string) string {
	t.Helper()
	a := strings.Index(src, from)
	if a < 0 {
		t.Fatalf("missing %q", from)
	}
	b := strings.Index(src[a:], to)
	if b < 0 {
		t.Fatalf("missing %q after %q", to, from)
	}
	return src[a : a+b]
}

// Behavioural: the real shared "add to finding" entry points, run with the gate
// off and on. api() is the observation point.
func TestFindingsAddToFindingEntryPointsHonourGate(t *testing.T) {
	src := readUIAsset(t, "js/findings.js")
	entry := strings.ReplaceAll(sliceBetween(t, src, "export function addFlowToFinding(", "// One set of defaults for every finding created"), "export ", "")
	pick := strings.ReplaceAll(sliceBetween(t, src, "export function pickFindingForFlows(", "function renderFindingPicker("), "export ", "")
	attach := sliceBetween(t, src, "async function attachFlowsToFinding(", "async function addPoCFlowsToFinding(")
	create := sliceBetween(t, src, "async function createFindingFromFlows(", "// pickFindingForFlows is the single")
	script := `
const MSG=` + "`" + findingsOffMessage + "`" + `;
let gate=false, apiCalls=[], toasts=[], modals=0, prompts=0;
const state={selected:new Set([9])}, findings=[];
let findingPickEpoch=0; const findingAttachPending=new Set();
const findingsEditable=()=>gate;
const FINDINGS_EDITING_OFF=MSG;
const assertFindingsWritable=()=>{ if(!gate)throw new Error(MSG); };
const requireFindingsEditing=()=>{ if(gate)return true; toasts.push(MSG); return false; };
const toast=m=>{toasts.push(m);return {}};
const toastError=(p,e)=>toasts.push(p+': '+(e&&e.message));
const api=async(path,opts)=>{apiCalls.push((opts&&opts.method||'GET')+' '+path);return {id:7,findings:[]};};
const node={style:{display:'flex'}};
const $=()=>node;
const openModal=()=>{modals++}, closeModal=()=>{};
const uiPrompt=async()=>{prompts++;return 'title'};
const flowFindingDefaults=()=>({}), flowOrigin=()=>'', loadFindings=()=>{}, toastOpenFinding=()=>{};
const renderFindingPicker=()=>{}, sevClass=()=>'', statusLabel=()=>'', findingPocCount=()=>0;
const findingMutationBlocked=()=>false, settleFindingBodyBeforeEvidence=async()=>{}, applyFindingEvidenceResponse=()=>{};
` + attach + `
` + create + `
` + entry + `
` + pick + `
const writes=()=>apiCalls.filter(c=>!c.startsWith('GET'));
const reset=()=>{apiCalls=[];toasts=[];modals=0;prompts=0;};
const done=async()=>new Promise(r=>setTimeout(r,5));

gate=false; reset();
pickFindingForFlows([1,2]); addFlowToFinding(4); pickFindingForSelection();
const r1=await attachFlowsToFinding(3,[1,2]);
await createFindingFromFlows([1],{});
await done();
if(apiCalls.length||modals||prompts)throw Error('gate off must do nothing: '+JSON.stringify({apiCalls,modals,prompts}));
if(r1&&r1.attached)throw Error('attachFlowsToFinding reported attached while off');
if(!toasts.length||toasts.some(m=>m!==MSG))throw Error('want only the off toast, got '+JSON.stringify(toasts));

gate=true; reset();
pickFindingForFlows([1,2]); await done();
if(!apiCalls.includes('GET /api/findings')||!modals)throw Error('gate on: picker must open and load findings '+JSON.stringify({apiCalls,modals}));
reset();
await attachFlowsToFinding(3,[1,2]);
if(writes().filter(c=>c==='POST /api/findings/3/flows').length!==2)throw Error('gate on: attach must POST per flow '+JSON.stringify(apiCalls));
reset();
await createFindingFromFlows([1],{});
if(!writes().includes('POST /api/findings'))throw Error('gate on: create must POST '+JSON.stringify(apiCalls));
`
	nodeModule(t, script)
}

// Behavioural: the real attach/revision modules against a stub DOM.
func TestFindingsEvidenceAndRevisionModulesHonourGate(t *testing.T) {
	if out, err := exec.Command("node", "--test", "ui/_js-tests/findings-readonly-gate.test.mjs").CombinedOutput(); err != nil {
		t.Fatalf("findings-readonly-gate.test.mjs failed:\n%s", out)
	}
}

// Source guard: every mutating call into the findings API must sit in a
// function that checks the gate. Fails loudly when a new ungated write appears,
// in ANY ui module, not only the three findings modules.
func TestFindingsWritesAreGated(t *testing.T) {
	gateRe := regexp.MustCompile(`findingsEditable\(\)|requireFindingsEditing\(|assertFindingsWritable\(`)
	callRe := regexp.MustCompile("\\bapi\\(\\s*['\"`]/api/(findings|finding-revisions)")
	mutRe := regexp.MustCompile(`method\s*:\s*['"](POST|PATCH|PUT|DELETE)`)
	files, err := filepath.Glob("ui/js/*.js")
	if err != nil || len(files) == 0 {
		t.Fatalf("no ui modules: %v", err)
	}
	// Writes whose path is not a literal at the call site must be gated inside
	// these named functions instead.
	dynamic := map[string]string{
		"ui/js/evidence-attach.js": "async function send(",
		"ui/js/notes.js":           "export async function promoteNoteSelection(",
		"ui/js/evidence-render.js": "async function attach(",
	}
	for file, marker := range dynamic {
		b, _ := os.ReadFile(file)
		src := string(b)
		i := strings.Index(src, marker)
		if i < 0 {
			t.Errorf("%s: dynamic writer %q moved; update the guard", file, marker)
			continue
		}
		body := src[i:]
		if j := regexp.MustCompile(`\n(async function|function|export )`).FindStringIndex(body[len(marker):]); j != nil {
			body = body[:len(marker)+j[0]]
		}
		if !gateRe.MatchString(body) {
			t.Errorf("%s: %s performs a finding write but has no findingsEditable()/requireFindingsEditing()/assertFindingsWritable() gate", file, marker)
		}
	}
	for _, file := range files {
		b, _ := os.ReadFile(file)
		src := executableJS(string(b))
		for _, m := range callRe.FindAllStringIndex(src, -1) {
			open := strings.Index(src[m[0]:], "(") + m[0]
			depth, end := 0, len(src)
			for i := open; i < len(src); i++ {
				if src[i] == '(' {
					depth++
				} else if src[i] == ')' {
					depth--
					if depth == 0 {
						end = i
						break
					}
				}
			}
			if !mutRe.MatchString(src[m[0]:end]) {
				continue // a read
			}
			// Window: up to 30 lines back, never past the enclosing top-level function.
			start := strings.LastIndex(src[:m[0]], "\n")
			for n := 0; n < 30 && start > 0; n++ {
				prev := strings.LastIndex(src[:start], "\n")
				line := src[prev+1 : start]
				if regexp.MustCompile(`^(export )?(async )?function `).MatchString(line) {
					start = prev
					break
				}
				start = prev
			}
			if start < 0 {
				start = 0
			}
			if !gateRe.MatchString(src[start:m[0]]) {
				line := strings.Count(src[:m[0]], "\n") + 1
				t.Errorf("%s:%d: ungated finding write %q (add findingsEditable()/requireFindingsEditing()/assertFindingsWritable() in the same function)", file, line, strings.TrimSpace(src[m[0]:min(m[0]+70, len(src))]))
			}
		}
	}
}

// Behavioural (H4): switching editing off with a draft and a debounced save
// pending must resolve them, not strand them behind the project-switch guard.
func TestFindingsEditingOffResolvesPendingDrafts(t *testing.T) {
	src := readUIAsset(t, "js/findings.js")
	workspace := readUIAsset(t, "js/finding-workspace.js")
	workspace = strings.ReplaceAll(workspace[strings.Index(workspace, "export function createFindingDraftStore"):], "export ", "")
	listener := sliceBetween(t, src, "window.addEventListener('interseptor:findings-ui-editing'", "\n});") + "\n});"
	discard := sliceBetween(t, src, "function discardPendingFindingChanges(", "\n}\n") + "\n}\n"
	enqueue := sliceBetween(t, src, "function enqueueFindingPatch(", "\nasync function drainFindingWrites(")
	script := workspace + `
let gate=true, fetches=0, renders=0, detailRenders=0, toasts=[];
const MSG='off';
const findingsEditable=()=>gate, FINDINGS_EDITING_OFF=MSG;
const assertFindingsWritable=()=>{ if(!gate)throw new Error(MSG); };
const findingDrafts=createFindingDraftStore(), cvssPreviewDrafts=createFindingDraftStore();
const bodySaveTimers=new Map(), bodySaveSnapshots=new Map(), cvssApplyDraftTokens=new Map(), findingWriteQueues=new Map();
let findEditMode=true, findingDetailRefreshDeferred=false, renderedFindingKey='x', selFinding=1;
const syncFindingsEditChrome=()=>{}, renderFindings=()=>{renders++}, renderFindingDetail=()=>{detailRenders++};
const findingMutationBlocked=()=>false, findingWriteQueue=()=>({latest:{},latestValues:{},pendingWaiters:[]}), drainFindingWrites=()=>{fetches++};
const toast=m=>toasts.push(m);
const $=sel=>sel==='#panel-findings'?{classList:{contains:()=>true}}:null;
const handlers={}; const window={addEventListener:(n,fn)=>{handlers[n]=fn;}};
` + enqueue + `
` + discard + `
` + listener + `
const guard=()=>findingDrafts.hasAny()||cvssPreviewDrafts.hasAny()||bodySaveTimers.size;

// a staged draft, a debounced body save and a CVSS preview are all pending
findingDrafts.stage(1,{title:'edited'});
bodySaveTimers.set(1,setTimeout(()=>{fetches++},20)); bodySaveSnapshots.set(1,[]);
cvssPreviewDrafts.stage(1,{vector:'AV:N'});
if(!guard())throw Error('precondition');

gate=false;
handlers['interseptor:findings-ui-editing']();
if(findEditMode)throw Error('editor must close when editing is switched off');
if(bodySaveTimers.size||bodySaveSnapshots.size)throw Error('debounced save must be cancelled');
if(!findingDrafts.failed(1))throw Error('draft must be marked failed so Retry is reachable after re-enable');
if(!renders)throw Error('must re-render immediately, not defer');
if(findingDetailRefreshDeferred)throw Error('must not defer behind the draft');
await new Promise(r=>setTimeout(r,40));
if(fetches)throw Error('a cancelled save must not write');

// new writes are refused at call time
let rejected=null; try{ await enqueueFindingPatch(1,{title:'x'}); }catch(e){ rejected=e; }
if(!rejected||rejected.message!==MSG)throw Error('enqueueFindingPatch must reject while off');
if(fetches)throw Error('enqueue must not start a drain while off');

// Discard resolves the pending state and releases the project-switch guard
discardPendingFindingChanges(1);
if(guard())throw Error('discard must release the project-switch guard');
if(!detailRenders||!toasts.length)throw Error('discard must re-render and tell the user');
`
	nodeModule(t, script)
}

// The controls that only exist to write a finding are removed, not left dead.
func TestFindingsWriteControlsAreHiddenWhenEditingIsOff(t *testing.T) {
	css := readUIAsset(t, "findings.css")
	if !strings.Contains(css, `:root:not([data-findings-editing="on"]) .findings-write{display:none}`) {
		t.Error("findings.css must hide .findings-write unless editing is on")
	}
	index := readUIAsset(t, "index.html")
	for _, id := range []string{"selAddFinding", "inspectAddFinding", "intrToFinding", "findDeletedOpen", "notesPromote", "evRenderAttach"} {
		m := regexp.MustCompile(`<button[^>]*\bid="` + id + `"[^>]*>`).FindString(index)
		if m == "" {
			t.Errorf("index.html lost #%s", id)
		} else if !strings.Contains(m, "findings-write") {
			t.Errorf("#%s must carry findings-write so it is hidden when editing is off", id)
		}
	}
	for _, id := range []string{"findNew", "findEmptyNew", "findAddFlow"} {
		if !strings.Contains(index+readUIAsset(t, "js/findings.js"), `id="`+id+`"`) {
			t.Errorf("pinned id #%s must remain", id)
		}
	}
	for file, want := range map[string]string{
		"js/tools.js":     `class="btn xs findings-write" id="repAddFinding"`,
		"js/flowmodal.js": "finding.className='btn findings-write'",
		"js/scanner.js":   `class="btn accent findings-write" id="scanPromote"`,
		"js/proxy.js":     "if(findingsEditable())fitems.push({label:'Add to finding'",
		"js/findings.js":  "document.documentElement.dataset.findingsEditing",
	} {
		if !strings.Contains(readUIAsset(t, file), want) {
			t.Errorf("%s missing %q", file, want)
		}
	}
	// No inline styles may have crept into index.html.
	if regexp.MustCompile(`\sstyle=`).MatchString(index) {
		t.Error("index.html must not use a style attribute")
	}
}
