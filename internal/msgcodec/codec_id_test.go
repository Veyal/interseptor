package msgcodec

import "testing"

// A codec is addressed by its file id (<id>.star): a copy saved under a new
// name with the original meta id must report the new file id so encode,
// repeater and collections load the same file that decoded the message.
func TestCodecIDIsFileIDNotMetaID(t *testing.T) {
	src := `
meta = {"id": "upstream-codec", "apply_on_send": True}
def match(flow, side): return True
def decode(flow, side, raw): return raw
def encode(flow, side, plaintext): return plaintext
`
	c, err := Compile("myapp", src)
	if err != nil {
		t.Fatal(err)
	}
	if c.Meta.ID != "myapp" {
		t.Fatalf("Meta.ID=%q, want file id myapp", c.Meta.ID)
	}
	if c.Meta.Title != "upstream-codec" {
		t.Fatalf("Title=%q, want the meta id as display fallback", c.Meta.Title)
	}
	r, ok := TryDecode([]*Codec{c}, Flow{ReqBody: "x"}, "req")
	if !ok || r.CodecID != "myapp" {
		t.Fatalf("TryDecode CodecID=%q ok=%v, want myapp", r.CodecID, ok)
	}
	dir := t.TempDir()
	if err := Save(dir, "myapp", src); err != nil {
		t.Fatal(err)
	}
	if list := List(dir); len(list) != 1 || list[0].Meta.ID != "myapp" {
		t.Fatalf("List meta=%+v", list)
	}
}
