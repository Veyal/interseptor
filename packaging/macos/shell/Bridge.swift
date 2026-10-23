import WebKit

/// Native side of the web-UI bridge.
///
/// UI → native: `window.webkit.messageHandlers.interseptor.postMessage(obj)`
/// with `{type:'ready'|'theme'|'status'|'drag-regions'|'file-accept'|'check-updates', …}`.
/// Native → UI: `window.interseptorDesktop.run(command)`, queued until the
/// page has said it is ready, and the `--titlebar-inset-left` CSS variable
/// (the width the traffic lights take from the page's title toolbar), set at
/// document start and again whenever it changes (full screen hides the lights).
@MainActor
final class DesktopBridge: NSObject, WKScriptMessageHandler {
    static let handlerName = "interseptor"

    weak var webView: WKWebView?
    var onTheme: ((String, String) -> Void)?
    var onStatus: ((DesktopStatus) -> Void)?
    var onReady: (() -> Void)?
    var onDragRegions: ((WindowDragWebView.Regions) -> Void)?
    var onFileAccept: (([String]) -> Void)?
    /// The user clicked the version badge: run a user-initiated update check.
    var onCheckUpdates: (() -> Void)?

    private var ready = false
    private var queue: [String] = []

    static func userScript(version: String, titlebarInset: CGFloat) -> WKUserScript {
        let source = """
        document.documentElement.classList.add('desktop-app','desktop-macos');
        \(insetScript(titlebarInset))
        window.interseptorDesktop={platform:'macos',version:\(jsString(version)),titlebar:'unified',post:(m)=>window.webkit.messageHandlers.interseptor.postMessage(m)};
        """
        return WKUserScript(source: source, injectionTime: .atDocumentStart, forMainFrameOnly: true)
    }

    /// JavaScript that sets the traffic-light inset on the root element.
    static func insetScript(_ inset: CGFloat) -> String {
        "document.documentElement.style.setProperty('--titlebar-inset-left','\(Int(inset.rounded()))px');"
    }

    /// Encodes s as a JavaScript string literal.
    static func jsString(_ s: String) -> String {
        guard let data = try? JSONSerialization.data(withJSONObject: [s]),
              let arr = String(data: data, encoding: .utf8) else { return "\"\"" }
        return String(arr.dropFirst().dropLast())
    }

    /// A new document is loading: its bridge is not ready until it says so.
    func pageWillLoad() { ready = false }

    /// Drops commands queued for the page that was loading: the target server
    /// changed, and a queued ⌘I meant for the old one must not toggle
    /// intercept on the new one once it is ready.
    func discardPending() { queue.removeAll() }

    func run(_ command: String) {
        guard ready, let webView else {
            queue.append(command)
            return
        }
        let js = "window.interseptorDesktop&&window.interseptorDesktop.run&&window.interseptorDesktop.run(\(Self.jsString(command)))"
        webView.evaluateJavaScript(js, completionHandler: nil)
    }

    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.frameInfo.isMainFrame, let body = message.body as? [String: Any],
              let type = body["type"] as? String else { return }
        switch type {
        case "ready":
            ready = true
            onReady?()
            let pending = queue
            queue.removeAll()
            pending.forEach(run)
        case "theme":
            if let bg = body["background"] as? String {
                onTheme?(bg, body["scheme"] as? String ?? "dark")
            }
        case "status":
            onStatus?(DesktopStatus(message: body))
        case "drag-regions":
            onDragRegions?(WindowDragWebView.Regions(drag: Self.rects(body["rects"]), exclude: Self.rects(body["exclude"])))
        case "file-accept":
            onFileAccept?((body["accept"] as? [Any] ?? []).compactMap { $0 as? String })
        case "check-updates":
            onCheckUpdates?()
        default:
            break
        }
    }

    /// Reads [{x,y,w,h}] in CSS pixels, dropping malformed or empty entries and
    /// capping the count so a misbehaving page cannot grow the list unbounded.
    static func rects(_ value: Any?) -> [CGRect] {
        guard let list = value as? [Any] else { return [] }
        return list.prefix(64).compactMap { item in
            guard let r = item as? [String: Any],
                  let x = (r["x"] as? NSNumber)?.doubleValue, let y = (r["y"] as? NSNumber)?.doubleValue,
                  let w = (r["w"] as? NSNumber)?.doubleValue, let h = (r["h"] as? NSNumber)?.doubleValue,
                  [x, y, w, h].allSatisfy({ $0.isFinite }), w > 0, h > 0 else { return nil }
            return CGRect(x: x, y: y, width: w, height: h)
        }
    }
}
