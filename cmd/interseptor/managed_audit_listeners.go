package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	managedUIAuditControlFDEnv     = "INTERSEPTOR_UI_AUDIT_CONTROL_FD"
	managedUIAuditProxyFDEnv       = "INTERSEPTOR_UI_AUDIT_PROXY_FD"
	startupListenerShutdownTimeout = 5 * time.Second
)

type managedUIAuditListeners struct {
	control net.Listener
	proxies []net.Listener
}

func (l *managedUIAuditListeners) close() {
	if l == nil {
		return
	}
	if l.control != nil {
		_ = l.control.Close()
	}
	for _, listener := range l.proxies {
		if listener != nil {
			_ = listener.Close()
		}
	}
}

// startRuntimeListeners uses descriptor-backed listeners only for the managed
// browser audit. All normal launches keep the existing bind/rebind path.
func startRuntimeListeners(pm *proxyManager, cm *controlManager, controlAddr string, proxyAddrs []string) error {
	inherited, err := loadManagedUIAuditListeners(controlAddr, proxyAddrs)
	if err != nil {
		return fmt.Errorf("managed UI audit listeners: %w", err)
	}
	if inherited == nil {
		if err := pm.StartAddrs(proxyAddrs); err != nil {
			return fmt.Errorf("proxy listen on %s: %w", strings.Join(proxyAddrs, ", "), err)
		}
		if err := cm.Start(controlAddr); err != nil {
			shutdownProxyAfterStartupFailure(pm)
			return fmt.Errorf("control listen on %s: %w", controlAddr, err)
		}
		return nil
	}

	defer inherited.close()
	if err := pm.StartListeners(proxyAddrs, inherited.proxies); err != nil {
		return fmt.Errorf("proxy inherit on %s: %w", strings.Join(proxyAddrs, ", "), err)
	}
	inherited.proxies = nil // proxy manager owns them now
	if err := cm.StartListener(controlAddr, inherited.control); err != nil {
		shutdownProxyAfterStartupFailure(pm)
		return fmt.Errorf("control inherit on %s: %w", controlAddr, err)
	}
	inherited.control = nil // control manager owns it now
	return nil
}

func shutdownProxyAfterStartupFailure(pm *proxyManager) {
	ctx, cancel := context.WithTimeout(context.Background(), startupListenerShutdownTimeout)
	defer cancel()
	pm.Shutdown(ctx)
}
