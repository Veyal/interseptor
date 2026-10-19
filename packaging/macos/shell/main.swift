// Entry point of the native Interseptor.app shell. The shell owns the window,
// menus, status item and notifications; the Go supervisor
// (interseptor-macapp --shell) owns the server process.
import AppKit

MainActor.assumeIsolated {
    let app = NSApplication.shared
    let delegate = AppDelegate()
    app.delegate = delegate
    app.setActivationPolicy(.regular)
    app.run()
}
