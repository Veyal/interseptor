package msgcodec

import (
	"strings"
	"testing"
)

// A codec that runs away must name the budget it hit; the library's own
// "too many steps" gives no scale.
func TestStepBudgetFailureReportsTheLimit(t *testing.T) {
	src := `
def match(flow, side):
    return True

def decode(flow, side, raw):
    x = 0
    for i in range(100000000):
        x = x + i
    return str(x)
`
	c, err := Compile("budget-hog", src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	res, err := c.Decode(Flow{Method: "GET", Host: "example.com"}, "req")
	if err == nil {
		t.Fatal("expected a budget failure")
	}
	if !strings.Contains(res.Error, "too many steps") || !strings.Contains(res.Error, "of 5000000 steps") {
		t.Fatalf("error should name the budget: %q", res.Error)
	}
	if !strings.Contains(err.Error(), "of 5000000 steps") {
		t.Fatalf("returned error should name the budget: %v", err)
	}
}

func TestMaxStepsIsExported(t *testing.T) {
	if MaxSteps != 5_000_000 {
		t.Fatalf("MaxSteps = %d", MaxSteps)
	}
}
