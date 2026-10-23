import AppKit
import UniformTypeIdentifiers
import WebKit

/// Native replacements for browser chrome: JS dialogs as sheets, file pickers
/// for <input type=file>, and window.open routed out of the app.
@MainActor
final class WebUIDelegate: NSObject, WKUIDelegate {
    weak var window: NSWindow?
    var navigator: Navigator?
    var downloads: DownloadController?

    /// The accept list of the file input the page is about to open, reported
    /// just before the picker by {type:'file-accept'}: WKOpenPanelParameters has
    /// no accept types. It applies to the next open panel only, and only when
    /// it is fresh, so a stale report never filters an unrelated picker.
    private var pendingAccept: (tokens: [String], at: Date)?
    static let acceptFreshness: TimeInterval = 3

    func noteFileAccept(_ tokens: [String]) { pendingAccept = (tokens, Date()) }

    private func takeAccept() -> [String] {
        defer { pendingAccept = nil }
        guard let p = pendingAccept, Date().timeIntervalSince(p.at) < Self.acceptFreshness else { return [] }
        return p.tokens
    }

    /// Maps accept tokens (".har", ".tar.gz", "application/json", "image/*")
    /// to content types. Returns nil, meaning any file, when the list is empty
    /// or any token has no type: a too-narrow filter would hide a file the
    /// page can read, while a too-wide one only shows extra files.
    static func contentTypes(forAccept tokens: [String]) -> [UTType]? {
        var types: [UTType] = []
        for raw in tokens {
            let t = raw.trimmingCharacters(in: .whitespaces).lowercased()
            var type: UTType?
            if t.hasPrefix(".") {
                // A multi-part extension (".tar.gz") matches on its last part.
                let ext = String(t.dropFirst()).split(separator: ".").last.map(String.init) ?? ""
                type = ext.isEmpty ? nil : UTType(filenameExtension: ext)
            } else if t.hasSuffix("/*") {
                switch t {
                case "image/*": type = .image
                case "text/*": type = .text
                case "audio/*": type = .audio
                case "video/*": type = .movie
                default: type = nil
                }
            } else if t.contains("/") {
                type = UTType(mimeType: t)
            }
            guard let type else { return nil }
            types.append(type)
        }
        return types.isEmpty ? nil : types
    }

    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                 for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        guard let url = action.request.url else { return nil }
        let localScheme = Navigator.isLocalScheme(url) && url.scheme?.lowercased() != "about"
        let sameOriginAPI = navigator?.isAllowed(url) == true && url.path.hasPrefix("/api/")
        if localScheme || sameOriginAPI {
            // A blob/data URL or an API export opened in a new tab is a file the
            // user wants, not a page: save it.
            webView.startDownload(using: action.request) { [weak self] d in d.delegate = self?.downloads }
        } else if ExternalLinks.canOpenExternally(url) {
            NSWorkspace.shared.open(url)
        }
        return nil
    }

    func webView(_ webView: WKWebView, runJavaScriptAlertPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping () -> Void) {
        let alert = makeAlert(message)
        alert.addButton(withTitle: "OK")
        present(alert) { _ in completionHandler() }
    }

    func webView(_ webView: WKWebView, runJavaScriptConfirmPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping (Bool) -> Void) {
        let alert = makeAlert(message)
        alert.addButton(withTitle: "OK")
        alert.addButton(withTitle: "Cancel")
        present(alert) { completionHandler($0 == .alertFirstButtonReturn) }
    }

    func webView(_ webView: WKWebView, runJavaScriptTextInputPanelWithPrompt prompt: String,
                 defaultText: String?, initiatedByFrame frame: WKFrameInfo,
                 completionHandler: @escaping (String?) -> Void) {
        let alert = makeAlert(prompt)
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 300, height: 24))
        field.stringValue = defaultText ?? ""
        alert.accessoryView = field
        alert.addButton(withTitle: "OK")
        alert.addButton(withTitle: "Cancel")
        alert.window.initialFirstResponder = field
        present(alert) { completionHandler($0 == .alertFirstButtonReturn ? field.stringValue : nil) }
    }

    func webView(_ webView: WKWebView, runOpenPanelWith parameters: WKOpenPanelParameters,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping ([URL]?) -> Void) {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = parameters.allowsMultipleSelection
        panel.canChooseDirectories = parameters.allowsDirectories
        panel.canChooseFiles = true
        let accept = takeAccept()
        if !parameters.allowsDirectories, let types = Self.contentTypes(forAccept: accept) {
            panel.allowedContentTypes = types
        }
        let done: (NSApplication.ModalResponse) -> Void = { completionHandler($0 == .OK ? panel.urls : nil) }
        if let window, window.isVisible {
            panel.beginSheetModal(for: window, completionHandler: done)
        } else {
            done(panel.runModal())
        }
    }

    private func makeAlert(_ text: String) -> NSAlert {
        let alert = NSAlert()
        alert.messageText = "Interseptor"
        alert.informativeText = text
        return alert
    }

    private func present(_ alert: NSAlert, _ done: @escaping (NSApplication.ModalResponse) -> Void) {
        if let window, window.isVisible {
            alert.beginSheetModal(for: window, completionHandler: done)
        } else {
            done(alert.runModal())
        }
    }
}
