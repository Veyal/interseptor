package control

import (
	"os/exec"
	"testing"
)

// After a reload a tab's earlier sends live only in IndexedDB. Before this fix
// they were read only when the history rail was opened, so the count read
// "History" and Diff vs previous stayed disabled even with earlier sends.
func TestUIRepeaterLoadsStoredHistoryWithTheRailClosed(t *testing.T) {
	source := readUIAsset(t, "js/tools.js")
	script := repeaterRenderJS(t, source, "function refreshRepHistory(t=repCur())") + `
let active={tid:1,history:[]},visible=false,counts=[],synced=0,hydrations=0,loads=0;
const repCur=()=>active,repHistoryVisible=()=>visible,loadRepHistory=()=>{loads++};
const repSetHistoryCount=t=>counts.push(t.history.length),syncRepActions=()=>{synced++};
function repHydrateTabHistory(t){hydrations++;t.historyHydrationPromise=Promise.resolve().then(()=>{t.history=[{id:3},{id:2}];});return t.historyHydrationPromise;}
refreshRepHistory(active);
await active.historyHydrationPromise;await Promise.resolve();
if(hydrations!==1||counts.at(-1)!==2||synced!==1)throw Error('closed rail must still load stored sends: '+JSON.stringify({hydrations,counts,synced}));
refreshRepHistory(active);
if(hydrations!==1)throw Error('hydration must run once per tab');
visible=true;refreshRepHistory(active);
if(loads!==1)throw Error('an open rail still reloads the list');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("Repeater history hydration: %v\n%s", err, out)
	}
}
