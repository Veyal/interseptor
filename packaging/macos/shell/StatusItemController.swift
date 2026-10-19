import AppKit

/// The menu-bar extra: always present while the app runs (including with the
/// window closed), showing the held-request count and quick toggles.
@MainActor
final class StatusItemController: NSObject {
    private let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    private let menu = NSMenu()
    private let interceptRequests = MainMenu.command("Intercept Requests", "intercept:toggle-request")
    private let interceptResponses = MainMenu.command("Intercept Responses", "intercept:toggle-response")
    private let projectLine = NSMenuItem(title: "Starting…", action: nil, keyEquivalent: "")
    private let liveLine = NSMenuItem(title: "", action: nil, keyEquivalent: "")

    override init() {
        super.init()
        item.button?.image = StatusGlyph.image()
        item.button?.imagePosition = .imageLeading
        item.button?.toolTip = "Interseptor"

        menu.addItem(NSMenuItem(title: "Open Interseptor", action: #selector(AppDelegate.showMainWindow(_:)), keyEquivalent: ""))
        menu.addItem(.separator())
        menu.addItem(interceptRequests)
        menu.addItem(interceptResponses)
        menu.addItem(.separator())
        projectLine.isEnabled = false
        liveLine.isEnabled = false
        liveLine.isHidden = true
        menu.addItem(projectLine)
        menu.addItem(liveLine)
        menu.addItem(.separator())
        menu.addItem(NSMenuItem(title: "Quit Interseptor", action: #selector(NSApplication.terminate(_:)), keyEquivalent: ""))
        item.menu = menu
    }

    func update(_ s: DesktopStatus) {
        interceptRequests.state = s.interceptRequest ? .on : .off
        interceptResponses.state = s.interceptResponse ? .on : .off
        projectLine.title = s.project.isEmpty ? "Interseptor" : "Project: \(s.project)"
        var live = s.liveLabel
        if s.held > 0 { live += " · \(s.held) held" }
        liveLine.title = live
        liveLine.isHidden = false
        item.button?.title = s.held > 0 ? " \(s.held)" : ""
        item.button?.toolTip = s.held > 0 ? "Interseptor — \(s.held) held" : "Interseptor"
    }

    func setStateLine(_ text: String) {
        projectLine.title = text
        liveLine.isHidden = true
    }
}
