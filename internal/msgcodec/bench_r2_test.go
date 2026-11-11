package msgcodec

import (
	"testing"

	"go.starlark.net/starlark"
)

const r2BenchCodec = `
def match(flow, side):
    return flow.host == "example.com" and side == "req"

def decode(flow, side, raw):
    return raw
`

// BenchmarkR2Match is one guarded codec call, the unit TryDecode repeats per
// codec per flow.
func BenchmarkR2Match(b *testing.B) {
	c, err := Compile("bench", r2BenchCodec)
	if err != nil {
		b.Fatal(err)
	}
	f := Flow{Host: "example.com"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if ok, err := c.Match(f, "req"); err != nil || !ok {
			b.Fatal(ok, err)
		}
	}
}

// BenchmarkR2MatchUnguarded is the same call without the wall-clock and memory
// guard, i.e. what Match cost before the bounds were added.
func BenchmarkR2MatchUnguarded(b *testing.B) {
	c, err := Compile("bench", r2BenchCodec)
	if err != nil {
		b.Fatal(err)
	}
	args := starlark.Tuple{flowValue(Flow{Host: "example.com"}), starlark.String("req")}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		thread := &starlark.Thread{Name: "match:bench"}
		thread.SetMaxExecutionSteps(maxSteps)
		if _, err := starlark.Call(thread, c.fnM, args, nil); err != nil {
			b.Fatal(err)
		}
	}
}
