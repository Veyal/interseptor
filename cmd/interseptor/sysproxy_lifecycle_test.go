package main

import "testing"

type fakeSysProxyOwner struct {
	recovered []bool
	released  int
}

func (f *fakeSysProxyOwner) RecoverSystemProxy(projectSwitch bool) error {
	f.recovered = append(f.recovered, projectSwitch)
	return nil
}

func (f *fakeSysProxyOwner) ReleaseSystemProxy() error {
	f.released++
	return nil
}

func TestRecoverSystemProxyHonoursProjectSwitch(t *testing.T) {
	f := &fakeSysProxyOwner{}
	t.Setenv(envProjectSwitch, "")
	recoverSystemProxy(f)
	t.Setenv(envProjectSwitch, "1")
	recoverSystemProxy(f)
	if len(f.recovered) != 2 || f.recovered[0] || !f.recovered[1] {
		t.Fatalf("recover calls = %v, want [false true]", f.recovered)
	}
	releaseSystemProxy(f)
	if f.released != 1 {
		t.Fatalf("release calls = %d, want 1", f.released)
	}
}
