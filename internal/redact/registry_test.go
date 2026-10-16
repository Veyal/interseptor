package redact

import (
	"strings"
	"sync"
	"testing"
)

func TestRegistryMask(t *testing.T) {
	r := NewRegistry()
	if r.Add("short") {
		t.Fatal("values under MinSecretLen must be ignored")
	}
	const canary = "CANARY-secret-0001"
	if !r.Add(canary) || !r.Add(canary+"-extended") {
		t.Fatal("expected tracked")
	}
	if r.Len() != 2 {
		t.Fatalf("len=%d", r.Len())
	}
	out := r.Mask("a=" + canary + "-extended b=" + canary)
	if strings.Contains(out, "CANARY") {
		t.Fatalf("leak: %s", out)
	}
	if strings.Count(out, "[redacted") != 2 {
		t.Fatalf("longest-first masking expected two placeholders: %s", out)
	}
	if !r.Contains(canary) || r.Contains("nothing") {
		t.Fatal("Contains wrong")
	}
	if r.Mask("short here") != "short here" {
		t.Fatal("short value must not mask")
	}
}

func TestRegistryNilAndConcurrent(t *testing.T) {
	var nilReg *Registry
	if nilReg.Add("secret-value") || nilReg.Mask("x") != "x" || nilReg.Len() != 0 || nilReg.Contains("x") {
		t.Fatal("nil registry must be inert")
	}
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.Add(strings.Repeat("k", 6+i))
			_ = r.Mask("kkkkkkkkkkkk")
		}(i)
	}
	wg.Wait()
}
