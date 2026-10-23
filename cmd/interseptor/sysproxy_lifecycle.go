package main

import (
	"log"
	"os"
)

// systemProxyOwner is the hub's system-proxy lifecycle surface.
type systemProxyOwner interface {
	RecoverSystemProxy(projectSwitch bool) error
	ReleaseSystemProxy() error
}

// recoverSystemProxy turns off an OS proxy left pointing at a dead listener by
// a crashed/killed previous run. A project-switch re-exec keeps it.
func recoverSystemProxy(h systemProxyOwner) {
	if err := h.RecoverSystemProxy(os.Getenv(envProjectSwitch) != ""); err != nil {
		log.Printf("system proxy: could not restore stale setting: %v", err)
	}
}

// releaseSystemProxy turns the OS proxy back off on graceful shutdown when
// Interseptor enabled it, so quitting never leaves the machine proxied to a
// closed port.
func releaseSystemProxy(h systemProxyOwner) {
	if err := h.ReleaseSystemProxy(); err != nil {
		log.Printf("system proxy: could not disable on shutdown: %v", err)
	}
}
