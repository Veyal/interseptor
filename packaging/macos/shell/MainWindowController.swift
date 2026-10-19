import AppKit
import WebKit

/// The single Interseptor window: a WKWebView on the control UI, with a native
/// loading/error overlay, chrome coloured to match the UI theme.
///
/// The title bar is unified with the page's 38px title toolbar (Xcode/Zed
/// style): the content view fills the whole window, the native title is hidden
/// (NSWindow.title is still kept current for Mission Control, the Window menu
/// and accessibility), and an empty compact toolbar gives the title bar the
/// toolbar's height so the traffic lights sit centred in it. The page reserves
/// --titlebar-inset-left for the lights and reports its drag regions.
@MainActor
final class MainWindowController: NSWindowController, NSWindowDelegate {
    let webView: WindowDragWebView
    let bridge = DesktopBridge()
    let navigator = Navigator()
    let uiDelegate = WebUIDelegate()
    let downloads = DownloadController()

    private let stateView = StateView()
    private var theme = Theme.initial
    private var titleObservation: NSKeyValueObservation?
    private var loadedURL: URL?
    private let version: String
    private var titlebarInset: CGFloat = 0

    var onRetry: (() -> Void)?
    var onUseLocal: (() -> Void)?

    init(version: String) {
        self.version = version
        let config = WKWebViewConfiguration()
        config.applicationNameForUserAgent = "InterseptorDesktop/\(version)"
        config.userContentController.add(bridge, name: DesktopBridge.handlerName)
        config.preferences.javaScriptCanOpenWindowsAutomatically = true
        webView = WindowDragWebView(frame: .zero, configuration: config)

        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1280, height: 820),
                              styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                              backing: .buffered, defer: false)
        window.title = "Interseptor"
        window.titlebarAppearsTransparent = true
        window.titleVisibility = .hidden
        let toolbar = NSToolbar(identifier: "InterseptorTitleBar")
        toolbar.showsBaselineSeparator = false
        toolbar.allowsUserCustomization = false
        toolbar.displayMode = .iconOnly
        window.toolbar = toolbar
        if #available(macOS 11.0, *) {
            window.toolbarStyle = .unifiedCompact
            window.titlebarSeparatorStyle = .none
        }
        window.minSize = NSSize(width: 900, height: 600)
        window.collectionBehavior.insert(.fullScreenPrimary)
        window.isReleasedWhenClosed = false
        window.tabbingMode = .disallowed
        super.init(window: window)

        window.delegate = self
        configureWebView()
        layout(in: window)
        window.center()
        window.setFrameAutosaveName("InterseptorMainWindow")
        updateTitlebarInset()
        apply(theme)
        showLoading()
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    private func configureWebView() {
        webView.setValue(false, forKey: "drawsBackground")
        webView.allowsBackForwardNavigationGestures = false
        webView.allowsMagnification = false
        webView.navigationDelegate = navigator
        webView.uiDelegate = uiDelegate
        bridge.webView = webView
        navigator.downloads = downloads
        uiDelegate.navigator = navigator
        uiDelegate.downloads = downloads
        uiDelegate.window = window
        downloads.window = window
        titleObservation = webView.observe(\.title, options: [.new]) { [weak self] wv, _ in
            DispatchQueue.main.async { self?.syncTitle(wv.title) }
        }
        bridge.onDragRegions = { [weak self] in self?.webView.regions = $0 }
        bridge.onFileAccept = { [weak self] in self?.uiDelegate.noteFileAccept($0) }
        navigator.onStart = { [weak self] in
            self?.bridge.pageWillLoad()
            self?.webView.regions = .init()
        }
        navigator.onFinish = { [weak self] in self?.stateView.isHidden = true }
        navigator.onFail = { [weak self] error in self?.showLoadFailure(error) }
    }

    private func layout(in window: NSWindow) {
        let content = NSView()
        for v in [webView, stateView] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            content.addSubview(v)
            NSLayoutConstraint.activate([
                v.leadingAnchor.constraint(equalTo: content.leadingAnchor),
                v.trailingAnchor.constraint(equalTo: content.trailingAnchor),
                v.topAnchor.constraint(equalTo: content.topAnchor),
                v.bottomAnchor.constraint(equalTo: content.bottomAnchor),
            ])
        }
        window.contentView = content
    }

    // MARK: - State

    func showLoading(_ text: String = "Starting proxy…") {
        stateView.resetTitle()
        stateView.showLoading(text)
        stateView.isHidden = false
    }

    func showFailure(title: String, message: String, actions: [StateView.Action]) {
        stateView.showError(title: title, message: message, actions: actions)
        stateView.isHidden = false
    }

    /// Loads the UI of the control server at base. The whole control origin is
    /// allowed in the web view, whatever UI path is configured.
    func load(_ base: URL, remote: Bool) {
        let url = Settings.uiURL(base: base)
        loadedURL = url
        navigator.allowedOrigin = Origin(url)
        window?.subtitle = remote ? (base.host ?? "") : ""
        webView.load(URLRequest(url: url))
    }

    /// Blanks the web view, e.g. before switching servers.
    func unload() {
        loadedURL = nil
        navigator.allowedOrigin = nil
        webView.loadHTMLString("", baseURL: nil)
    }

    private func showLoadFailure(_ error: Error) {
        guard loadedURL != nil else { return }
        var actions = [StateView.Action(title: "Retry") { [weak self] in self?.reloadFromStart() }]
        if Settings.remoteURL != nil {
            actions.insert(StateView.Action(title: "Use Local Proxy") { [weak self] in self?.onUseLocal?() }, at: 0)
        }
        showFailure(title: "Could not load Interseptor",
                    message: error.localizedDescription, actions: actions)
    }

    /// Retry after a failed page load: reload the URL that failed. A fresh
    /// request is needed (WKWebView.reload does nothing after a provisional
    /// failure). The error screen goes away when the navigation finishes.
    /// Only without a URL (nothing was ever loaded) is a full reconnect asked for.
    private func reloadFromStart() {
        if reloadLoadedURL() { return }
        onRetry?()
    }

    /// True when a URL was loaded and has been requested again.
    @discardableResult
    func reloadLoadedURL() -> Bool {
        guard let url = loadedURL else { return false }
        showLoading("Connecting…")
        webView.load(URLRequest(url: url))
        return true
    }

    var hasLoadedURL: Bool { loadedURL != nil }

    // MARK: - Unified title bar

    /// Space from the window's left edge to just past the zoom button, plus the
    /// gap the page leaves before its first control; 0 in full screen, where
    /// the lights hide with the title bar.
    private func measureTitlebarInset() -> CGFloat {
        guard let window, !window.styleMask.contains(.fullScreen),
              let zoom = window.standardWindowButton(.zoomButton), let superview = zoom.superview else { return 0 }
        let frame = superview.convert(zoom.frame, to: nil)
        return (frame.maxX + 8).rounded()
    }

    /// Re-measures the inset and hands it to the page: the document-start
    /// script carries it into every new document, and the current document
    /// gets it at once.
    func updateTitlebarInset(force: Bool = false) {
        let inset = measureTitlebarInset()
        guard force || inset != titlebarInset || webView.configuration.userContentController.userScripts.isEmpty else { return }
        titlebarInset = inset
        let controller = webView.configuration.userContentController
        controller.removeAllUserScripts()
        controller.addUserScript(DesktopBridge.userScript(version: version, titlebarInset: inset))
        webView.evaluateJavaScript(DesktopBridge.insetScript(inset), completionHandler: nil)
    }

    /// In full screen the empty toolbar would stay pinned as a blank strip over
    /// the page's title toolbar, so it is hidden there; the menu bar and the
    /// lights then reveal on hover as for any full-screen window.
    func windowWillEnterFullScreen(_ notification: Notification) {
        window?.toolbar?.isVisible = false
        updateTitlebarInset()
    }

    func windowDidEnterFullScreen(_ notification: Notification) { updateTitlebarInset() }
    func windowWillExitFullScreen(_ notification: Notification) { window?.toolbar?.isVisible = true }
    func windowDidExitFullScreen(_ notification: Notification) {
        window?.toolbar?.isVisible = true
        updateTitlebarInset()
    }
    func windowDidResize(_ notification: Notification) { updateTitlebarInset() }

    // MARK: - Theme and title

    func applyTheme(background hex: String, scheme: String) {
        guard let color = NSColor(hex: hex) else { return }
        Settings.lastBackground = hex
        Settings.lastScheme = scheme
        apply(Theme(background: color, dark: scheme != "light"))
    }

    private func apply(_ t: Theme) {
        theme = t
        window?.backgroundColor = t.background
        window?.appearance = t.appearance
        webView.underPageBackgroundColor = t.background
        stateView.apply(t)
    }

    private func syncTitle(_ title: String?) {
        let t = (title ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        window?.title = t.isEmpty ? "Interseptor" : t
    }

    func showWindow() {
        if window?.isMiniaturized == true { window?.deminiaturize(nil) }
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func reload() {
        guard loadedURL != nil else { return }
        if webView.url == nil || webView.url?.absoluteString == "about:blank" {
            reloadLoadedURL()
        } else {
            webView.reload()
        }
    }

    func toggleInspector() -> Bool {
        if #available(macOS 13.3, *) {
            webView.isInspectable.toggle()
            return webView.isInspectable
        }
        let prefs = webView.configuration.preferences
        let on = !((prefs.value(forKey: "developerExtrasEnabled") as? Bool) ?? false)
        prefs.setValue(on, forKey: "developerExtrasEnabled")
        return on
    }
}
