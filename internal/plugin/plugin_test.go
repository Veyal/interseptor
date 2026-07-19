package plugin

import "testing"

func TestFlowHookFires(t *testing.T) {
	Reset()
	var got int64
	OnFlowCaptured(func(id int64) { got = id })
	EmitFlowCaptured(42)
	if got != 42 {
		t.Fatalf("got %d", got)
	}
}
