import AppKit
import WebKit

/// scheme://host:port, the unit the shell allows inside its web view.
struct Origin: Equatable {
    var scheme: String
    var host: String
    var port: Int

    init?(_ url: URL?) {
        guard let url, let scheme = url.scheme?.lowercased(), let host = url.host?.lowercased() else { return nil }
        self.scheme = scheme
        self.host = host
        self.port = url.port ?? (scheme == "https" ? 443 : 80)
    }
}

/// Keeps the web view on the control origin. Everything else — external links,
/// docs, target=_blank — goes to the default browser; attachments and
/// non-displayable responses become native downloads.
@MainActor
final class Navigator: NSObject, WKNavigationDelegate {
    var allowedOrigin: Origin?
    var downloads: DownloadController?
    var onStart: (() -> Void)?
    var onFinish: (() -> Void)?
    var onFail: ((Error) -> Void)?
    /// The web content process kept crashing; automatic reloads stopped.
    var onCrashLoop: (() -> Void)?
    private var crashReloads = ReloadBudget()

    /// Gives automatic crash recovery a fresh budget (an explicit retry).
    func resetCrashBudget() { crashReloads.reset() }

    static func isLocalScheme(_ url: URL?) -> Bool {
        guard let s = url?.scheme?.lowercased() else { return true }
        return s == "about" || s == "blob" || s == "data"
    }

    func isAllowed(_ url: URL?) -> Bool {
        Self.isLocalScheme(url) || (allowedOrigin != nil && Origin(url) == allowedOrigin)
    }

    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        let url = action.request.url
        if action.shouldPerformDownload {
            decisionHandler(.download)
            return
        }
        let mainFrame = action.targetFrame?.isMainFrame ?? true
        let scheme = url?.scheme?.lowercased()
        if mainFrame && (scheme == "blob" || scheme == "data") {
            // Navigating the app's own frame to a blob/data URL would replace
            // the UI with a file: save the file instead.
            decisionHandler(.download)
            return
        }
        if isAllowed(url) {
            decisionHandler(.allow)
            return
        }
        // Only a top-level navigation is the user's intent to go somewhere; a
        // foreign sub-frame load is just blocked, as is any scheme that would
        // launch an arbitrary app.
        if mainFrame, let url, ExternalLinks.canOpenExternally(url) {
            NSWorkspace.shared.open(url)
        }
        decisionHandler(.cancel)
    }

    func webView(_ webView: WKWebView, decidePolicyFor response: WKNavigationResponse,
                 decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void) {
        let disposition = (response.response as? HTTPURLResponse)?
            .value(forHTTPHeaderField: "Content-Disposition")?.lowercased() ?? ""
        if !response.canShowMIMEType || disposition.hasPrefix("attachment") {
            decisionHandler(.download)
        } else {
            decisionHandler(.allow)
        }
    }

    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        download.delegate = downloads
    }

    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        download.delegate = downloads
    }

    func webView(_ webView: WKWebView, didStartProvisionalNavigation navigation: WKNavigation!) {
        onStart?()
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        onFinish?()
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        reportFailure(error)
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        reportFailure(error)
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        if crashReloads.allow() {
            webView.reload()
        } else {
            onCrashLoop?()
        }
    }

    private func reportFailure(_ error: Error) {
        let ns = error as NSError
        // A cancelled load (superseded navigation) or a navigation that turned
        // into a download is not a failure.
        if ns.domain == NSURLErrorDomain && ns.code == NSURLErrorCancelled { return }
        if ns.domain == "WebKitErrorDomain" && (ns.code == 102 || ns.code == 204) { return }
        onFail?(error)
    }
}
