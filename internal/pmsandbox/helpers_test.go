package pmsandbox

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/scriptctx"
)

var fixedNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// base returns a deterministic trusted-script input.
func base(phase scriptctx.Phase, script string) Input {
	r := rand.New(rand.NewPCG(1, 2))
	return Input{
		Phase: phase, Script: script, Caps: scriptctx.DefaultCaps(),
		Clock: func() time.Time { return fixedNow }, Rand: r.Float64,
		Request:  &scriptctx.Request{Name: "Login", Method: "POST", URL: "https://api.example.com/v1/login?a=1", Headers: []scriptctx.Header{{Key: "Content-Type", Value: "application/json"}}},
		Response: nil,
	}
}

func jsonResp(code int, body string) *scriptctx.Response {
	return &scriptctx.Response{
		Code: code, Status: map[int]string{200: "OK", 201: "Created", 401: "Unauthorized", 404: "Not Found"}[code],
		Headers:      []scriptctx.Header{{Key: "Content-Type", Value: "application/json"}, {Key: "X-Request-Id", Value: "abc"}},
		Body:         []byte(body),
		ResponseTime: 42 * time.Millisecond,
	}
}

func run(t *testing.T, in Input) Output {
	t.Helper()
	out := Run(context.Background(), in)
	return out
}

func mustPass(t *testing.T, out Output) {
	t.Helper()
	if out.Status != StatusPass {
		t.Fatalf("status=%s errors=%+v tests=%+v unsupported=%v", out.Status, out.Errors, out.Tests, out.Unsupported)
	}
}
