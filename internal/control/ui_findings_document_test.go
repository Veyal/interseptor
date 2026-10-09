package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// findingDocumentHarness loads the pure workspace + document modules (no imports,
// no DOM) so the document model runs under node exactly as the browser loads it.
func findingDocumentHarness(t *testing.T, body string) string {
	t.Helper()
	src := readUIAsset(t, "js/finding-workspace.js") + "\n" + readUIAsset(t, "js/finding-document.js")
	return strings.ReplaceAll(src, "export ", "") + `
function equal(a,b){if(JSON.stringify(a)!==JSON.stringify(b))throw Error(JSON.stringify({a,b}));}
function has(h,s,m){if(!String(h).includes(s))throw Error((m||'missing')+': '+s+'\n'+h);}
function lacks(h,s,m){if(String(h).includes(s))throw Error((m||'must not contain')+': '+s+'\n'+h);}
` + body
}

func runFindingDocumentScript(t *testing.T, name, body string) {
	t.Helper()
	if out, err := exec.Command("node", "--input-type=module", "-e", findingDocumentHarness(t, body)).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", name, err, out)
	}
}

// Legacy section links (#finding-N/overview|evidence|remediation|review) and
// readiness gap links must keep landing somewhere sensible now that the four
// panels are one document: sections are anchors, remediation/review open the
// Report prep fold. flow-N keeps opening the flow.
func TestFindingDocumentRouteMapping(t *testing.T) {
	runFindingDocumentScript(t, "route mapping", `
equal(findingRouteTarget(parseFindingRoute('#finding-4/overview')),{anchor:'claim',openPrep:false,flowId:null});
equal(findingRouteTarget(parseFindingRoute('#finding-4')),{anchor:'claim',openPrep:false,flowId:null});
equal(findingRouteTarget(parseFindingRoute('#finding-4/evidence')),{anchor:'reproduction',openPrep:false,flowId:null});
equal(findingRouteTarget(parseFindingRoute('#finding-4/remediation')),{anchor:'report-prep',openPrep:true,flowId:null});
equal(findingRouteTarget(parseFindingRoute('#finding-4/review')),{anchor:'report-prep',openPrep:true,flowId:null});
equal(findingRouteTarget(parseFindingRoute('#finding-4/flow-9')),{anchor:'reproduction',openPrep:false,flowId:9});
equal(findingRouteTarget(parseFindingRoute('#finding-4/unknown')),null);
equal(findingRouteTarget(parseFindingRoute('#finding-0')),null);
equal(findingRouteTarget({section:'review'}).anchor,'report-prep');
// Bare section names (used by gap links and the readiness meter) map the same way.
equal(findingRouteTarget({section:findingSectionForGap('retest')}),{anchor:'report-prep',openPrep:true,flowId:null});
equal(findingRouteTarget({section:findingSectionForGap('proof')}).anchor,'reproduction');
equal(findingRouteTarget({section:findingSectionForGap('summary')}).anchor,'claim');
equal(findingRouteTarget({section:findingSectionForGap('cvss')}).openPrep,true);
equal(findingRouteTarget({section:'nonsense'}),null);
equal(ANCHOR_IDS['report-prep'],'findReportPrep');
`)
}

func TestFindingDetailIsOneScrollingDocument(t *testing.T) {
	js := readUIAsset(t, "js/findings.js")
	for _, banned := range []string{"data-find-panel", "find-section-nav", "find-step-nav", "find-next-section", "find-proof-glance", "No impact written yet", "No concise claim yet", "function renderFindingProof", "function renderFindingStory"} {
		if strings.Contains(js, banned) {
			t.Errorf("findings.js still carries the tabbed-panel scaffolding %q", banned)
		}
	}
	requireUIContains(t, js, `id="findDocument"`, `id="findRail"`, `id="findReportPrep"`, "readDocumentHTML(", "readPropertiesHTML(", "findingRouteTarget(")
	if strings.Count(js, `class="find-workspace-content`) != 1 {
		t.Error("exactly one scroll container holds the document")
	}
	// Report prep is collapsed by default and holds the old Remediation + Review panels.
	prep := js[strings.Index(js, `id="findReportPrep"`):]
	if !regexp.MustCompile(`^id="findReportPrep"[^>]*>`).MatchString(prep) || strings.Contains(prep[:strings.Index(prep, ">")], " open") {
		t.Error("Report prep must be a details element that is closed by default")
	}
	requireUIContains(t, prep, "readinessMeterHTML(f.readiness", "id: 'findMeter'", "renderProofReview(", "renderFindingRevisions()", "machineProof")
	requireUIContains(t, js, `aria-label="Finding remediation"`, "fixBlock", "retestBlock")
	// The rail lists the properties; read-only renders plain values, edit renders controls.
	detail := js[strings.Index(js, "function renderFindingDetail("):]
	rail := detail[strings.Index(detail, `id="findRail"`):strings.Index(detail, `id="findReportPrep"`)]
	requireUIContains(t, rail, "readPropertiesHTML(", "propsEdit")
	requireUIContains(t, detail, `id="findStatus"`, `id="findConfidence"`, `id="findEnv"`, `id="findCwe"`, `id="findCvssEditor"`, `id="findEditTags"`)
}

func TestFindingDocumentSectionsRenderOnlyWithContent(t *testing.T) {
	runFindingDocumentScript(t, "section visibility", `
const base={id:3,severity:'High',status:'open',blocks:[],targets:[]};
const md=s=>'<p>'+s+'</p>';
// A claim alone: no impact, affected, reproduction, or stub.
let h=readDocumentHTML({...base,summary:'Any user can read other accounts.'},{renderMD:md});
has(h,'class="find-claim"');has(h,'Any user can read other accounts.');
for(const s of ['find-impact','find-aff','find-sec-poc','find-stub','IMPACT'])lacks(h,s);
lacks(h,'<h3','the claim needs no eyebrow label');
// Impact with root cause beneath, severity-coloured.
h=readDocumentHTML({...base,summary:'Claim.',impact:'Account takeover.',why:'Missing ownership check.'},{renderMD:md});
has(h,'class="find-impact find-sv-high"');has(h,'IMPACT');has(h,'Account takeover.');has(h,'find-why');has(h,'Missing ownership check.');
if(h.indexOf('find-impact')<h.indexOf('find-claim'))throw Error('impact must follow the claim');
if(h.indexOf('find-why')<h.indexOf('find-impact'))throw Error('root cause sits under impact');
// No placeholder sentences for missing fields.
for(const s of ['written yet','No impact','No concise','Not recorded','No remediation'])lacks(h,s);
// A single target never renders the Affected section.
h=readDocumentHTML({...base,summary:'C',targets:[{url:'https://example.com/a',methods:['GET'],flow_ids:[1]}]},{renderMD:md});
lacks(h,'find-aff');
// Steps produce the reproduction host.
h=readDocumentHTML({...base,summary:'C',blocks:[{type:'text',md:'Send it.',role:'action'}]},{renderMD:md});
has(h,'id="find-sec-poc"');has(h,'id="findBody"');has(h,'Reproduction');
`)
}

func TestFindingDocumentStubNamesTheAuthorOnce(t *testing.T) {
	runFindingDocumentScript(t, "stub", `
const md=s=>s;
let h=readDocumentHTML({id:9,source:'ai',severity:'Low',status:'open',blocks:[],targets:[]},{renderMD:md});
equal((h.match(/find-stub/g)||[]).length,1);
has(h,'role="status"');has(h,'agent');has(h,'claim');has(h,'reproduction');
for(const s of ['No impact written yet','find-claim"','find-sec-poc','find-impact'])lacks(h,s);
// Human-authored stub does not call itself an agent.
h=readDocumentHTML({id:9,source:'human',severity:'Low',status:'open'},{renderMD:md});
has(h,'find-stub');lacks(h,'authoring agent');
// A claim (or steps) removes it.
lacks(readDocumentHTML({id:9,source:'ai',summary:'x',severity:'Low'},{renderMD:md}),'find-stub');
lacks(readDocumentHTML({id:9,source:'ai',severity:'Low',blocks:[{type:'text',md:'step'}]},{renderMD:md}),'find-stub');
// Impact alone still shows beside the stub; the stub is one block.
h=readDocumentHTML({id:9,source:'ai',severity:'Low',impact:'Bad.'},{renderMD:md});
equal((h.match(/find-stub/g)||[]).length,1);has(h,'find-impact');
`)
}

func TestFindingReproductionTimelineAttachesEvidenceToSteps(t *testing.T) {
	runFindingDocumentScript(t, "timeline", `
const blocks=[
 {type:'text',md:'Log in as A.',role:'setup'},
 {type:'text',md:'Request B resource.',role:'action'},
 {type:'flow',flowId:11,role:'action',method:'GET',host:'example.com',path:'/r/2',status:200},
 {type:'text',md:'Body leaks.',role:'result'},
 {type:'image',hash:'abc',caption:'Leaked data',role:'result',source:'browser_screenshot'},
 {type:'flow',flowId:12,role:'control',method:'GET',host:'example.com',path:'/r/3',status:403,proof:'Control is denied'},
];
const steps=buildReproductionSteps(blocks);
equal(steps.map(s=>[s.n,s.role,s.evidence.map(e=>e.type+(e.flowId||e.hash))]),[[1,'setup',[]],[2,'action',['flow11']],[3,'result',['imageabc']],[4,'control',['flow12']]]);
equal(steps[3].md,'Control is denied');
const html=timelineHTML(steps,{renderMD:s=>s,evidenceHTML:(b)=>'<i data-ev="'+(b.flowId||b.hash)+'"></i>'});
const at=s=>html.indexOf(s);
// Evidence sits inside its own step, between that step's prose and the next step.
if(!(at('Request B resource.')<at('data-ev="11"')&&at('data-ev="11"')<at('Body leaks.')))throw Error('flow 11 detached from its step');
if(!(at('Body leaks.')<at('data-ev="abc"')&&at('data-ev="abc"')<at('Control is denied')))throw Error('screenshot detached from its step');
for(const r of ['SETUP','ACTION','RESULT','CONTROL'])has(html,r);
has(html,'id="find-step-2"');has(html,'data-step-flows="11"');
has(html,'<ol class="find-tl"');
equal((html.match(/find-node/g)||[]).length,4);
// Empty text blocks are not steps; no blocks, no timeline.
equal(buildReproductionSteps([{type:'text',md:'  '},{type:'text',md:''}]).length,0);
equal(timelineHTML([],{}),'');
// Evidence with no preceding step still gets one.
const lead=buildReproductionSteps([{type:'flow',flowId:5,role:'action',proof:'Sent it'}]);
equal(lead.length,1);equal(lead[0].md,'Sent it');
// A missing flow renders nothing and a missing screenshot keeps a visible gap.
equal(buildReproductionSteps([{type:'text',md:'s'},{type:'flow',flowId:1,missing:true}])[0].evidence.length,0);
`)
}

func TestFindingAffectedTargetsCarryTheirOwnEvidence(t *testing.T) {
	runFindingDocumentScript(t, "affected", `
const md=s=>s;
const f={id:7,severity:'Medium',status:'open',summary:'x',
 targets:[
  {url:'https://example.com/a',methods:['GET'],relation:'affected',flow_ids:[11,12]},
  {url:'https://example.com/b',methods:['POST'],relation:'affected',flow_ids:[]},
  {url:'https://example.com/c',methods:['PUT'],relation:'sink',image_hashes:['abc']},
  {url:'https://example.com/d',relation:'setup',evidenceException:'Setup only'}],
 blocks:[{type:'text',md:'A',role:'action'},{type:'flow',flowId:11,role:'action'},{type:'text',md:'B',role:'result'},{type:'flow',flowId:12,role:'result'}]};
const steps=buildReproductionSteps(f.blocks);
const rows=affectedRows(f,steps);
equal(rows.map(r=>[r.i,r.needs]),[[1,false],[2,true],[3,false],[4,false]]);
equal(rows[0].chips.map(c=>[c.flowId,c.step]),[[11,1],[12,2]]);
const h=readDocumentHTML(f,{renderMD:md});
has(h,'class="find-aff"');
equal((h.match(/class="find-aff-row/g)||[]).length,4);
// Chips jump to the step that proves the target.
has(h,'data-evref-flow="11"');has(h,'href="#find-step-1"');has(h,'href="#find-step-2"');
// A target with no evidence says so and carries the amber edge.
has(h,'needs evidence');has(h,'find-aff-row is-gap');
equal((h.match(/needs evidence/g)||[]).length,1);
// Chips for flows that are not in the document fall back to opening the flow.
const lone=affectedRows({id:7,targets:[{url:'u',flow_ids:[40]},{url:'v'}]},[]);
equal(lone[0].chips[0].step,null);
const hh=readDocumentHTML({id:7,severity:'Low',summary:'x',targets:[{url:'u',flow_ids:[40]},{url:'v'}]},{renderMD:md});
has(hh,'href="#finding-7/flow-40"');
// Server-owned gap indexes also mark a target.
equal(affectedRows({id:1,readiness:{targetEvidenceGaps:[0]},targets:[{url:'u',flow_ids:[1]},{url:'v',flow_ids:[2]}]},[]).map(r=>r.needs),[true,false]);
// Missing flow ids are labelled, not linked.
has(readDocumentHTML({id:7,severity:'Low',summary:'x',targets:[{url:'u',flow_ids:[40],missingFlowIds:[40]},{url:'v',flow_ids:[2]}]},{renderMD:md}),'missing');
`)
}

func TestFindingRailReadOnlyShowsPlainValues(t *testing.T) {
	runFindingDocumentScript(t, "rail", `
const f={id:2,severity:'Critical',status:'verified',confidence:'firm',cwe:'CWE-639',environment:'staging',cvss:'CVSS:4.0/AV:N/AC:L',cvssScore:9.3,tags:['api','idor']};
const h=readPropertiesHTML(f);
for(const s of ['Severity','Critical','Status','verified','Confidence','firm','CWE','CWE-639','Environment','staging','CVSS','9.3','CVSS:4.0/AV:N/AC:L','api','idor'])has(h,s);
for(const s of ['<select','<input','<textarea','<button','contenteditable'])lacks(h,s);
has(h,'<dl class="find-props"');
// Unset optional properties are omitted, never rendered as "Not set".
const bare=readPropertiesHTML({id:2,severity:'Low',status:'open'});
has(bare,'Severity');has(bare,'Status');for(const s of ['Confidence','CWE','Environment','CVSS','Tags','Not set'])lacks(bare,s);
`)
}

func TestFindingDocumentReadsEvidenceInlineFromTheFlow(t *testing.T) {
	js := readUIAsset(t, "js/findings.js")
	start := strings.Index(js, "function renderFindReportBody")
	if start < 0 {
		t.Fatal("renderFindReportBody missing")
	}
	body := js[start:]
	requireUIContains(t, body, "buildReproductionSteps(", "timelineHTML(", "find-xch", "data-xch-pane", "/raw?side=", "if (b.type === 'image')", "if (b.type === 'flow')", "find-open-flow", "copyText(")
	if strings.Index(body, "if (b.type === 'image')") > strings.Index(body, "if (b.type === 'flow')") {
		t.Error("image evidence branch must precede the flow branch")
	}
	css := readUIAsset(t, "findings.css")
	requireUIContains(t, css, "@container findws (min-width:900px)", "@container findws (max-width:1180px)", ".find-xchbd", ".find-tl", ".find-rail", "container-type:inline-size")
}

func TestFindingDocumentCSSDoesNotAnimateOrBreakTokens(t *testing.T) {
	css := readUIAsset(t, "findings.css")
	for _, sel := range []string{".find-claim", ".find-impact", ".find-tl", ".find-xch", ".find-aff", ".find-rail", ".find-stub", ".find-prep"} {
		if !strings.Contains(css, sel) {
			t.Errorf("findings.css missing %s", sel)
		}
	}
	if strings.Contains(css, ".find-section-nav") || strings.Contains(css, ".find-step-nav") {
		t.Error("tab strip styles must be removed with the tab strip")
	}
}
