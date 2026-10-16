package preview

import (
	"errors"
	"testing"
	"time"
)

func TestRenderFrameHonoursDeadline(t *testing.T) {
	_, err := RenderIntruderTimeline(tlFixture12(), Opts{Deadline: time.Now().Add(-time.Second)})
	if !errors.Is(err, ErrRenderTimeout) {
		t.Fatalf("expired deadline must abort, got %v", err)
	}
	if _, err := RenderIntruderTimeline(tlFixture12(), Opts{Deadline: time.Now().Add(time.Minute)}); err != nil {
		t.Fatalf("future deadline: %v", err)
	}
}
