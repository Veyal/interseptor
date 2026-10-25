package main

import (
	"errors"
	"log"
	"os"
	"strings"

	"github.com/Veyal/interseptor/internal/appwindow"
	"github.com/Veyal/interseptor/internal/pdfexport"
)

// envProjectSwitch marks a process started by a project-switch re-exec. The
// UI window that asked for the switch reconnects by itself, so a second one
// must not open.
const envProjectSwitch = "INTERSEPTOR_PROJECT_SWITCH"

// uiWindowSkip reports why the UI window should not open on start, or "" to
// open it. INTERSEPTOR_NO_BROWSER and --headless always win; --open overrides
// only the SSH guess (a shell over SSH would put the window on this machine's
// screen, not the operator's).
func uiWindowSkip(headless, force bool, getenv func(string) string) string {
	switch {
	case getenv("INTERSEPTOR_NO_BROWSER") != "":
		return "INTERSEPTOR_NO_BROWSER is set"
	case headless || getenv("INTERSEPTOR_HEADLESS") != "":
		return "headless"
	case force || getenv("INTERSEPTOR_OPEN_BROWSER") != "":
		return ""
	case getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "":
		return "started over SSH; pass --open to open it on this machine's display"
	}
	return ""
}

// openUIWindow opens the control UI in an app window, off the startup path.
// When that is skipped or impossible it logs the URL to open in a browser.
func openUIWindow(url string, headless, force bool) {
	if os.Getenv(envProjectSwitch) != "" {
		return
	}
	if why := uiWindowSkip(headless, force, os.Getenv); why != "" {
		log.Printf("UI window not opened (%s) — open %s in a browser", why, url)
		return
	}
	go func() {
		if err := appwindow.New().Open(url, appwindow.ProfileDir()); err != nil {
			if errors.Is(err, pdfexport.ErrNoBrowser) || strings.Contains(err.Error(), "no headless Chrome found") {
				// No Chromium browser for an app-style window: the UI works in
				// any browser, so open the default one.
				if oerr := openDefaultBrowser(url); oerr == nil {
					log.Printf("Opened %s in your default browser (no Chrome, Edge, Chromium or Brave for an app window; PDF export and the test browser need one: run 'interseptor install-chrome')", url)
					return
				}
				log.Printf("Could not open the UI window (no Chrome found: run 'interseptor install-chrome' to download it automatically, or set INTERSEPTOR_CHROME) — open %s in a browser", url)
			} else {
				log.Printf("Could not open the UI window (%v) — open %s in a browser", err, url)
			}
		}
	}()
}
