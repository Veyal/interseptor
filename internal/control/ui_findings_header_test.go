package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// findingHeaderMarkup returns the sticky header template literal body.
func findingHeaderMarkup(t *testing.T) string {
	t.Helper()
	js := readUIAsset(t, "js/findings.js")
	a := strings.Index(js, `<header class="find-header find-header-sticky">`)
	if a < 0 {
		t.Fatal("finding detail header not found")
	}
	b := strings.Index(js[a:], "</header>")
	if b < 0 {
		t.Fatal("finding detail header is not closed")
	}
	return js[a : a+b]
}

func TestFindingHeaderIsThreeRows(t *testing.T) {
	h := findingHeaderMarkup(t)
	top := strings.Index(h, `class="find-header-top"`)
	title := strings.Index(h, `class="find-title-row"`)
	ctx := strings.Index(h, `class="find-context-line"`)
	if top < 0 || title < 0 || ctx < 0 || !(top < title && title < ctx) {
		t.Fatalf("header must be status row, title row, context row in order (top=%d title=%d ctx=%d)", top, title, ctx)
	}
	row1 := h[top:title]
	for _, want := range []string{`find-id-badge`, `find-sev-chip`, `findingModeChipHTML()`, `id="findSaveState"`, `id="findToggleEdit"`, `id="findMore"`, `id="findBackToList"`} {
		if !strings.Contains(h, want) {
			t.Errorf("header missing %s", want)
		}
	}
	if !strings.Contains(row1, "#${f.id}") {
		t.Error("row 1 must lead with the dim #id")
	}
	if strings.Contains(h, "FINDING #") {
		t.Error("the FINDING #n badge must become a plain #n")
	}
	if !strings.Contains(h[title:ctx], `id="findTitleText"`) {
		t.Error("row 2 must carry the title")
	}
	if !strings.Contains(h[ctx:], "find-ctx-method") || !strings.Contains(h[ctx:], "f.environment") || !strings.Contains(h[ctx:], "f.cwe") {
		t.Error("row 3 must carry METHOD url, environment and CWE")
	}
}

func TestFindingHeaderSeverityChipCarriesCVSSScore(t *testing.T) {
	h := findingHeaderMarkup(t)
	if !regexp.MustCompile("find-sev-chip[^`]*\\$\\{[^}]*cvssScore").MatchString(h) || !strings.Contains(h, "find-sev-score") {
		t.Error("severity chip must carry the CVSS score (cvssScore) when present")
	}
}

func TestFindingHeaderDropsReadinessAndDangerousActions(t *testing.T) {
	h := findingHeaderMarkup(t)
	for _, banned := range []string{"readinessMeterHTML", "findMeter", "find-stage-link", "findingReadinessLabel", `id="findDelete"`, `id="findCopyLink"`, `id="findPreviewChain"`, "btn danger", "Preview chain", "Copy link"} {
		if strings.Contains(h, banned) {
			t.Errorf("header must not contain %q (moved to More or Review)", banned)
		}
	}
	js := readUIAsset(t, "js/findings.js")
	// The meter still belongs on the Review panel.
	review := js[strings.Index(js, `data-find-panel="review"`):]
	requireUIContains(t, review, "readinessMeterHTML(f.readiness", "id: 'findMeter'")
}

func TestFindingMoreMenuHoldsSecondaryActions(t *testing.T) {
	js := readUIAsset(t, "js/findings.js")
	a := strings.Index(js, "async function confirmDeleteFinding(")
	b := strings.Index(js, "\nfunction openFindingMore(")
	if a < 0 || b < a {
		t.Fatal("More menu functions missing")
	}
	sections := js[a:b]
	requireUIContains(t, sections, "Copy link", "Preview chain", "Delete", "findingsEditable()", "danger: true", "uiConfirm(", "recover it from Deleted findings")
	if !strings.Contains(js, "openCtxMenu(") || !strings.Contains(js, "$('#findMore')") {
		t.Error("More trigger must be wired to openCtxMenu")
	}
}

// Runs the real handler: the trigger click must stop propagating before the
// menu opens, otherwise the app-wide click-to-close listener hides it again
// (the Proxy inspector bug fixed in caae4e3).
func TestFindingMoreTriggerStopsPropagationBeforeOpening(t *testing.T) {
	js := readUIAsset(t, "js/findings.js")
	start := strings.Index(js, "async function confirmDeleteFinding(")
	if start < 0 {
		t.Fatal("cannot extract More menu functions")
	}
	end := strings.Index(js[start:], "\nfunction openFindingMore(")
	end += strings.Index(js[start+end:], "\n}\n") + 3
	body := js[start : start+end]
	script := `
const log=[];let editable=true;
const findingsEditable=()=>editable,openCtxMenu=(x,y,s,t)=>log.push(['open',s,t]),toast=()=>{},copyText=()=>{},findingHref=()=>'#f',
 uiConfirm=async()=>false,esc=x=>x,import_=null,selFinding=1,visibleFindings=()=>[],deleteFinding=async()=>{},renderFindings=()=>{},loadFindings=async()=>{},
 toastError=()=>{},location={origin:'',pathname:''},$=()=>null;
` + body + `
const trigger={getBoundingClientRect:()=>({left:1,bottom:2})};
const f={id:1,title:'t',relatedFindings:[]};
openFindingMore({stopPropagation(){log.push(['stop'])}},trigger,f);
if(log[0][0]!=='stop'||log[1][0]!=='open'||log[1][2]!==trigger)throw Error('stopPropagation must precede openCtxMenu: '+JSON.stringify(log.map(x=>x[0])));
const labels=s=>s.flatMap(x=>x.items.filter(Boolean).map(i=>i.label));
let l=labels(log[1][1]);
for(const w of ['Copy link','Preview chain','Delete'])if(!l.includes(w))throw Error('missing '+w+' in '+l);
editable=false;l=labels(findingMoreSections(f));
if(l.includes('Delete'))throw Error('Delete must be hidden when findings are not editable');
if(!l.includes('Copy link'))throw Error('Copy link must remain in read-only mode');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("More handler: %v\n%s", err, out)
	}
}
