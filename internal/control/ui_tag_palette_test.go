package control

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func TestUITagPaletteActionsMatchColorAPI(t *testing.T) {
	src := readUIAsset(t, "js/tags.js")
	paletteStart := strings.Index(src, "export const TAG_COLORS")
	paletteEnd := strings.Index(src, "let tagLoadError")
	menuStart := strings.Index(src, "function openColorMenu(")
	menuEnd := strings.Index(src, "async function setTagColor(")
	if paletteStart < 0 || paletteEnd <= paletteStart || menuStart < 0 || menuEnd <= menuStart {
		t.Fatal("tag palette actions missing")
	}
	script := strings.ReplaceAll(src[paletteStart:paletteEnd], "export ", "") + `
const state={tagColors:{}},sent=[];let sections;
const openCtxMenu=(x,y,s)=>sections=s,filterByTag=()=>{};
const setTagColor=(tag,color)=>sent.push({tag,color});
` + src[menuStart:menuEnd] + `
openColorMenu(0,0,'generic');
for(const item of sections[0].items)item.act();
process.stdout.write(JSON.stringify(sent));`
	out, err := exec.Command("node", "-e", script).Output()
	if err != nil {
		t.Fatalf("run palette actions: %v", err)
	}
	var actions []struct {
		Tag   string `json:"tag"`
		Color string `json:"color"`
	}
	if err := json.Unmarshal(out, &actions); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 8 {
		t.Fatalf("want seven presets and Clear, got %d actions", len(actions))
	}
	h, st, _ := newHub(t)
	fid, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/", Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetFlowTags(fid, []string{"generic"}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	for _, action := range actions {
		t.Run(action.Color, func(t *testing.T) {
			body, _ := json.Marshal(action)
			req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/tags/generic/color", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			res, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusNoContent {
				response, _ := io.ReadAll(res.Body)
				t.Fatalf("palette sent %q, API returned %d: %s", action.Color, res.StatusCode, response)
			}
			tags, err := st.DistinctTags()
			if err != nil {
				t.Fatal(err)
			}
			if len(tags) != 1 || tags[0].Tag != action.Tag || tags[0].Color != action.Color {
				t.Fatalf("saved palette choice = %+v, want %+v", tags, action)
			}
		})
	}
}
