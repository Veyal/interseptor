package main

import (
	"context"
	"reflect"
	"testing"
)

type shutdownRecorder struct {
	name  string
	calls *[]string
}

func (r shutdownRecorder) Shutdown(context.Context) {
	*r.calls = append(*r.calls, r.name)
}

type closeRecorder struct {
	calls *[]string
}

func (r closeRecorder) Close() {
	*r.calls = append(*r.calls, "hub")
}

func TestShutdownStopsControlBeforeClosingHub(t *testing.T) {
	var calls []string
	shutdownRuntime(
		context.Background(),
		shutdownRecorder{name: "control", calls: &calls},
		closeRecorder{calls: &calls},
		shutdownRecorder{name: "proxy", calls: &calls},
	)

	want := []string{"control", "hub", "proxy"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("shutdown order = %v, want %v", calls, want)
	}
}
