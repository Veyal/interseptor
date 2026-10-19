import AppKit
import WebKit

/// The web view of a window with a unified title bar. The page draws the title
/// toolbar under the traffic lights, so the native title bar no longer catches
/// drags: WKWebView ignores -webkit-app-region. Instead the page reports its
/// drag regions ({type:'drag-regions'}): rectangles in CSS pixels where a press
/// moves the window, minus the rectangles of the controls inside them. A
/// primary-button press inside a drag region and outside every control drags
/// the window natively; a double-click there zooms or minimises it as the user
/// chose in System Settings › Desktop & Dock. Every other press reaches the page.
final class WindowDragWebView: WKWebView {
    struct Regions: Equatable {
        var drag: [CGRect] = []
        var exclude: [CGRect] = []

        /// Whether a point in page coordinates (CSS px, origin top left) drags.
        func drags(_ p: CGPoint) -> Bool {
            drag.contains { $0.contains(p) } && !exclude.contains { $0.contains(p) }
        }
    }

    var regions = Regions()

    /// The window action for a title bar double-click, read the way AppKit's
    /// own title bar reads it: AppleActionOnDoubleClick ("Maximize" by default,
    /// "Minimize" or "None"), with the older AppleMiniaturizeOnDoubleClick.
    enum DoubleClickAction { case zoom, minimize, none }
    static func doubleClickAction(_ defaults: UserDefaults = .standard) -> DoubleClickAction {
        switch defaults.string(forKey: "AppleActionOnDoubleClick") {
        case "Minimize": return .minimize
        case "None": return .none
        case "Maximize", "Fill": return .zoom
        default:
            return defaults.bool(forKey: "AppleMiniaturizeOnDoubleClick") ? .minimize : .zoom
        }
    }

    /// The press location in page coordinates. WKWebView is not flipped, so
    /// y is measured down from the top of the view.
    private func pagePoint(_ event: NSEvent) -> CGPoint {
        let p = convert(event.locationInWindow, from: nil)
        return CGPoint(x: p.x, y: isFlipped ? p.y : bounds.height - p.y)
    }

    override func mouseDown(with event: NSEvent) {
        guard let window, event.type == .leftMouseDown, !event.modifierFlags.contains(.control),
              regions.drags(pagePoint(event)) else {
            super.mouseDown(with: event)
            return
        }
        if event.clickCount == 2 {
            switch Self.doubleClickAction() {
            case .zoom: window.zoom(nil)
            case .minimize: window.miniaturize(nil)
            case .none: break
            }
            return
        }
        window.performDrag(with: event)
    }
}
