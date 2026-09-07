package control

import (
	"os/exec"
	"strings"
	"testing"
)

func TestFindingsWorkspaceNavigationAndFiltering(t *testing.T) {
	src := readUIAsset(t, "js/finding-workspace.js") + readUIAsset(t, "js/surface-position.js")
	script := strings.ReplaceAll(src, "export ", "") + `
const findings=[
 {id:12,title:'Example issue',target:'example.com',severity:'High',status:'open',tags:['web'],summary:'Session setting'},
 {id:13,title:'Another issue',target:'example.org',targets:[{url:'https://example.org/a',methods:['GET']},{url:'https://example.net/secondary',methods:['PATCH'],role:'reviewer',variant:'record'}],severity:'Low',status:'fixed',tags:['api']}
];
function equal(a,b){if(JSON.stringify(a)!==JSON.stringify(b))throw Error(JSON.stringify({a,b}));}
equal(filterFindingRecords(findings,{query:'session',severity:'High'}).map(f=>f.id),[12]);
equal(filterFindingRecords(findings,{query:'#13',status:'fixed'}).map(f=>f.id),[13]);
equal(filterFindingRecords(findings,{tag:'api',severity:'High'}),[]);
equal(filterFindingRecords(findings,{query:'secondary PATCH reviewer record'}).map(f=>f.id),[13]);
equal(findingSectionForGap('target_evidence'),'overview');
equal(findingSectionForGap('execution'),'review');
equal(findingSectionForGap('cvss'),'review');
equal(findingSectionForGap('control'),'evidence');
equal(parseFindingRoute('#finding-12/evidence'),{id:12,section:'evidence',flowId:null});
equal(parseFindingRoute('#finding-12/flow-7'),{id:12,section:'evidence',flowId:7});
equal(parseFindingRoute('#finding-12'),{id:12,section:'overview',flowId:null});
equal(parseFindingRoute('#finding-12/unknown'),null);
equal(parseFindingRoute('#finding-0'),null);
equal(findingSectionForGap('proof'),'evidence');
equal(findingSectionForGap('retest'),'remediation');
equal(findingSectionForGap('confidence'),'review');
equal(findingSectionForGap('summary'),'overview');
const drafts=createFindingDraftStore();
if(drafts.hasAny())throw Error('empty draft store blocks switching');
const older=drafts.stage(12,{summary:'first',impact:'risk'});
const newer=drafts.stage(12,{summary:'second'});
drafts.fail(12,older);
equal(drafts.values(12),{summary:'second',impact:'risk'});
if(!drafts.failed(12))throw Error('failed field was not retained');
drafts.acknowledge(12,older);
equal(drafts.values(12),{summary:'second'});
const body=drafts.stage(13,{blocks:[{type:'text',md:'retained body'}]});
drafts.fail(13,body);
drafts.fail(12,newer);
if(!drafts.failed(12)||!drafts.failed(13))throw Error('failures must remain finding-owned');
const retry=drafts.stage(12,drafts.values(12));
drafts.fail(12,newer);
if(drafts.failed(12))throw Error('old rejection poisoned a newer retry');
drafts.acknowledge(12,retry);
equal(drafts.values(12),{});
if(!drafts.has(13))throw Error('other finding body was discarded');
if(!drafts.hasAny())throw Error('unselected finding draft would be lost on switch');
drafts.discard(13,'blocks');
if(drafts.has(13))throw Error('explicitly discarded draft retained');
if(drafts.hasAny())throw Error('acknowledged/discarded drafts still block switching');
const placement=placeFloatingSurface({left:375,right:390,top:720,bottom:752,width:15},280,260,{width:390,height:780});
if(placement.left<8||placement.left+placement.width>382||placement.top<8||placement.top+placement.maxHeight>772)throw Error('menu outside viewport');
if(placement.side!=='above')throw Error('bottom-edge menu should flip');
const top=placeFloatingSurface({left:15,right:200,top:5,bottom:35,width:185},200,260,{width:390,height:780});
if(top.side!=='below'||top.top!==41)throw Error('top menu misplaced');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("Findings workspace behavior: %v\n%s", err, out)
	}
}
