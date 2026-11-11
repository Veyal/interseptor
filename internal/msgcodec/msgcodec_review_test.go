package msgcodec

import (
	"strings"
	"sync"
	"testing"
)

const dialectCodec = `
def match(flow, side):
    return len(set([1, 1, 2])) == 2

def decode(flow, side, raw):
    return raw
`

// Compile no longer reads the process-wide resolver flags; the dialect must be
// exactly what it was: set() allowed, while loops and recursion rejected.
func TestCompileDialectIsExplicitAndUnchanged(t *testing.T) {
	c, err := Compile("dialect", dialectCodec)
	if err != nil {
		t.Fatalf("set() must stay available: %v", err)
	}
	if ok, err := c.Match(Flow{}, "req"); err != nil || !ok {
		t.Fatalf("match = %v, %v", ok, err)
	}
	for name, src := range map[string]string{
		"while":     "def match(flow, side):\n    while True:\n        pass\n    return True\n\ndef decode(flow, side, raw):\n    return raw\n",
		"recursion": "def f(n):\n    return f(n)\n\ndef match(flow, side):\n    return f(1)\n\ndef decode(flow, side, raw):\n    return raw\n",
		"toplevel":  "if True:\n    x = 1\n\ndef match(flow, side):\n    return True\n\ndef decode(flow, side, raw):\n    return raw\n",
	} {
		c, err := Compile(name, src)
		if err == nil {
			_, err = c.Match(Flow{}, "req")
		}
		if err == nil {
			t.Errorf("%s: accepted, the dialect widened", name)
		}
	}
}

// Codecs compile and run concurrently (History decode, Repeater, MCP); run
// under -race.
func TestConcurrentCompileAndDecode(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := Compile("dialect", dialectCodec)
			if err != nil {
				t.Error(err)
				return
			}
			r, err := c.Decode(Flow{ReqBody: "x"}, "req")
			if err != nil || !strings.Contains(r.Plaintext, "x") {
				t.Errorf("decode = %+v, %v", r, err)
			}
		}()
	}
	wg.Wait()
}
