import AppKit

/// Wires the pieces together and owns the app lifecycle: closing the window
/// keeps the proxy running, ⌘Q stops it gracefully, and a dead supervisor turns
/// into an in-window error instead of a silent hang.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSMenuItemValidation {
    static let helpURL = URL(string: "https://github.com/Veyal/interseptor")!
    /// Longer than the supervisor's own SIGTERM→SIGKILL window (6s).
    static let quitTimeout: TimeInterval = 8

    private var windowController: MainWindowController!
    private var mainMenu: MainMenu!
    private var updater: Updater!
    private var statusItem: StatusItemController!
    private let supervisor = Supervisor()
    private let notifier = Notifier()
    private var status = DesktopStatus()
    private var quitting = false
    private var inspectorOn = false
    /// The supervisor's last INTERSEPTOR_ERROR line; more specific than its exit status.
    private var lastError: String?

    private var version: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "dev"
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        updater = Updater(currentVersion: version, bundleID: Bundle.main.bundleIdentifier ?? "com.veyal.interseptor",
                          bundleURL: Bundle.main.bundleURL)
        mainMenu = MainMenu()
        NSApp.mainMenu = mainMenu.menu
        statusItem = StatusItemController()
        windowController = MainWindowController(version: version)
        wireBridge()
        supervisor.onEvent = { [weak self] in self?.handle($0) }
        notifier.onOpenIntercept = { [weak self] in
            self?.windowController.showWindow()
            self?.windowController.bridge.run("panel:intercept")
        }
        windowController.onRetry = { [weak self] in self?.connect() }
        windowController.onUseLocal = { [weak self] in self?.useLocal() }
        windowController.showWindow()
        connect()
        updater.start()
    }

    private func wireBridge() {
        let bridge = windowController.bridge
        bridge.onTheme = { [weak self] bg, scheme in self?.windowController.applyTheme(background: bg, scheme: scheme) }
        bridge.onStatus = { [weak self] in self?.apply($0) }
        bridge.onCheckUpdates = { [weak self] in self?.updater.check(userInitiated: true) }
    }

    // MARK: - Connecting

    /// Loads the remote server when one is configured, otherwise (re)starts the
    /// local supervisor and waits for its READY line.
    private func connect() {
        if let remote = Settings.remoteURL {
            statusItem.setStateLine("Connecting to \(remote.host ?? "server")…")
            windowController.showLoading("Connecting to \(remote.host ?? "server")…")
            windowController.load(remote, remote: true)
            return
        }
        if supervisor.isRunning {
            // Already up (e.g. Retry after a page-load failure): reload what
            // was loaded instead of leaving the error screen in place.
            windowController.reloadLoadedURL()
            return
        }
        windowController.showLoading()
        statusItem.setStateLine("Starting proxy…")
        lastError = nil // a previous run's error must not explain this one's exit
        let exe = Bundle.main.executableURL!.deletingLastPathComponent().appendingPathComponent("interseptor-macapp")
        do {
            try supervisor.start(executable: exe, environment: Settings.supervisorEnvironment())
        } catch {
            showSupervisorFailure("Could not start the Interseptor supervisor: \(error.localizedDescription)")
        }
    }

    private func handle(_ event: Supervisor.Event) {
        switch event {
        case .ready(let url):
            lastError = nil
            windowController.load(url, remote: false)
        case .error(let message):
            // May arrive after .exited (the supervisor prints it, then exits,
            // and the exit can win the race): it then replaces the generic
            // "stopped unexpectedly" text with the real cause.
            guard !quitting else { return }
            lastError = message
            showSupervisorFailure(message)
        case .exited(let code):
            guard !quitting else { return }
            showSupervisorFailure(lastError ?? "The proxy stopped unexpectedly (exit status \(code)).")
            lastError = nil
        }
    }

    private func showSupervisorFailure(_ message: String) {
        statusItem.setStateLine("Proxy stopped")
        windowController.showFailure(title: "Interseptor stopped", message: message, actions: [
            .init(title: "Show Log") { [weak self] in self?.showLog(nil) },
            .init(title: "Restart") { [weak self] in self?.restart() },
        ])
    }

    private func restart() {
        supervisor.stop(timeout: Self.quitTimeout) { [weak self] in self?.connect() }
    }

    private func useLocal() {
        Settings.remoteURL = nil
        windowController.unload()
        connect()
    }

    // MARK: - Bridge status

    private func apply(_ s: DesktopStatus) {
        notifier.heldChanged(s, appActive: NSApp.isActive)
        status = s
        mainMenu.interceptRequests.state = s.interceptRequest ? .on : .off
        mainMenu.interceptResponses.state = s.interceptResponse ? .on : .off
        statusItem.update(s)
        NSApp.dockTile.badgeLabel = s.held > 0 ? "\(s.held)" : nil
    }

    // MARK: - Lifecycle

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        if !flag { windowController.showWindow() }
        return true
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        if quitting { return .terminateLater }
        if status.held > 0 {
            let alert = NSAlert()
            let n = status.held
            alert.messageText = "\(n) request\(n == 1 ? " is" : "s are") held in Intercept. Quit anyway?"
            alert.informativeText = "Held requests are dropped when the proxy stops."
            alert.addButton(withTitle: "Quit")
            alert.addButton(withTitle: "Cancel")
            alert.alertStyle = .warning
            if alert.runModal() != .alertFirstButtonReturn {
                updater.cancelPendingInstall()
                return .terminateCancel
            }
        }
        quitting = true
        supervisor.stop(timeout: Self.quitTimeout) {
            NSApp.reply(toApplicationShouldTerminate: true)
        }
        return .terminateLater
    }

    /// The server has stopped and the quit is certain: a pending update's
    /// helper may now start (it waits for this process to exit).
    func applicationWillTerminate(_ notification: Notification) {
        updater?.launchPendingInstall()
    }

    // MARK: - Actions

    @objc func checkForUpdates(_ sender: Any?) { updater.check(userInitiated: true) }

    @objc func toggleAutomaticUpdateChecks(_ sender: Any?) { updater.automaticChecks.toggle() }

    @objc func runCommand(_ sender: NSMenuItem) {
        guard let command = sender.representedObject as? String else { return }
        if !command.hasPrefix("intercept:") { windowController.showWindow() }
        windowController.bridge.run(command)
    }

    @objc func showMainWindow(_ sender: Any?) { windowController.showWindow() }

    @objc func reloadPage(_ sender: Any?) { windowController.reload() }

    @objc func openHelp(_ sender: Any?) { NSWorkspace.shared.open(Self.helpURL) }

    @objc func showLog(_ sender: Any?) {
        let url = Supervisor.logURL
        if FileManager.default.fileExists(atPath: url.path) {
            NSWorkspace.shared.open(url)
        } else {
            NSWorkspace.shared.activateFileViewerSelecting([url.deletingLastPathComponent()])
        }
    }

    @objc func toggleInspector(_ sender: NSMenuItem) {
        inspectorOn = windowController.toggleInspector()
        sender.state = inspectorOn ? .on : .off
    }

    @objc func connectToServer(_ sender: Any?) {
        windowController.showWindow()
        ConnectSheet.present(on: windowController.window) { [weak self] choice in
            guard let self else { return }
            switch choice {
            case .remote(let url):
                Settings.remoteURL = url
                self.windowController.unload()
                // A remote server replaces the local one; stop what we started.
                self.supervisor.stop(timeout: Self.quitTimeout) { self.connect() }
            case .local:
                self.useLocal()
            }
        }
    }

    func validateMenuItem(_ item: NSMenuItem) -> Bool {
        if item.action == #selector(toggleInspector(_:)) { item.state = inspectorOn ? .on : .off }
        if item.action == #selector(toggleAutomaticUpdateChecks(_:)) { item.state = updater?.automaticChecks == true ? .on : .off }
        return true
    }
}
