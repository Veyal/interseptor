package preview

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
)

func TestFlowBandsDrawsDecodeNoticesAfterCaption(t *testing.T) {
	bands := flowBands("Highlighted: token", RenderResult{}, 0, []string{"Response decoded: base64", "Request decoded: form-urlencoded"})
	if len(bands) != 3 {
		t.Fatalf("bands = %+v", bands)
	}
	if bands[0].notice || bands[0].text != "Highlighted: token" {
		t.Fatalf("caption band = %+v", bands[0])
	}
	for i, want := range []string{"Response decoded: base64", "Request decoded: form-urlencoded"} {
		if !bands[i+1].notice || bands[i+1].text != want {
			t.Fatalf("notice %d = %+v, want %q", i, bands[i+1], want)
		}
	}
}

func TestFlowBandsClipsAndSkipsBlankNotices(t *testing.T) {
	bands := flowBands("", RenderResult{}, 0, []string{"  ", strings.Repeat("x", 400)})
	if len(bands) != 1 || len([]rune(bands[0].text)) > 160 {
		t.Fatalf("bands = %+v", bands)
	}
}

func TestRenderFlowWithNoticesStillRenders(t *testing.T) {
	req := []byte("POST /x HTTP/1.1\r\nHost: example.com\r\n\r\nname = Ada Lovelace\nrole = admin")
	res, err := RenderFlow(req, nil, Options{Side: SideReq, Notices: []string{"Request decoded: form-urlencoded"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(res.PNG)); err != nil {
		t.Fatal(err)
	}
	plain, err := RenderFlow(req, nil, Options{Side: SideReq})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(plain.PNG, res.PNG) {
		t.Fatal("notice band should change the image")
	}
}
