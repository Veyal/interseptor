package control

import (
	"os/exec"
	"strings"
	"testing"
)

func TestUISurfacePlacementFlipsAndClamps(t *testing.T) {
	src := strings.Replace(readUIAsset(t, "js/surface-position.js"), "export function", "function", 1)
	script := src + `
const below=placeFloatingSurface({left:360,top:110,bottom:130},240,180,{left:0,top:0,width:390,height:844});
if(below.left!==142||below.top!==136||below.maxHeight!==180||below.side!=='below')throw new Error('ordinary placement escaped viewport');
const above=placeFloatingSurface({left:300,top:760,bottom:780},240,180,{left:0,top:0,width:390,height:844});
if(above.side!=='above'||above.top<8||above.left<8||above.left+above.width>382)throw new Error('surface did not flip and clamp');
const narrow=placeFloatingSurface({left:-20,top:100,bottom:120},500,600,{left:0,top:0,width:320,height:200});
if(narrow.width!==304||narrow.maxHeight>184||narrow.left!==8)throw new Error('narrow viewport bounds were not respected');
for(const top of [-200,900]){
  const offset=placeFloatingSurface({left:0,top,bottom:top+32},400,300,{left:20,top:50,width:250,height:220});
  if(offset.left<28||offset.left+offset.width>262||offset.top<58||offset.top+offset.maxHeight>262)throw new Error('scrolled or zoomed anchor escaped visual viewport');
}`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("surface placement: %v\n%s", err, out)
	}
}

func TestUISharedSurfaceContracts(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	hints := readUIAsset(t, "js/hints.js")
	activity := readUIAsset(t, "js/activity.js")
	proxy := readUIAsset(t, "js/proxy.js")
	mapJS := readUIAsset(t, "js/map.js")
	css := readUIAsset(t, "surfaces.css")
	for _, want := range []string{"placeFloatingSurface", "aria-expanded", "aria-haspopup", "maxHeight"} {
		if !strings.Contains(core, want) {
			t.Errorf("core shared surface contract missing %q", want)
		}
	}
	for _, want := range []string{"coarsePointer", "show(e.target.closest?.('[data-tooltip]'),'pointer')"} {
		if !strings.Contains(hints, want) {
			t.Errorf("tooltip mobile contract missing %q", want)
		}
	}
	for _, want := range []string{"act-detail", "aria-expanded", "act-expandable"} {
		if !strings.Contains(activity, want) {
			t.Errorf("activity expansion contract missing %q", want)
		}
	}
	for _, want := range []string{"aria-valuemin", "aria-valuemax", "aria-valuenow", "openCtxMenu(r.left, r.bottom+2, sections, btn)"} {
		if !strings.Contains(proxy, want) {
			t.Errorf("proxy accessibility contract missing %q", want)
		}
	}
	for _, want := range []string{"tip.offsetHeight", "maxTop", "Math.min(maxTop"} {
		if !strings.Contains(mapJS, want) {
			t.Errorf("map tooltip clamp contract missing %q", want)
		}
	}
	for _, want := range []string{"#ctxmenu", "#toast", ".act-detail", ".modal-overlay > .modal-shell"} {
		if !strings.Contains(css, want) {
			t.Errorf("surface stylesheet missing %q", want)
		}
	}
}
