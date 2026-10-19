import AppKit

/// "Connect to Server…": points the shell at a remote control URL (for example
/// a Tailscale address) instead of a local proxy.
@MainActor
enum ConnectSheet {
    enum Choice {
        case remote(URL)
        case local
    }

    static func present(on window: NSWindow?, completion: @escaping (Choice) -> Void) {
        let alert = NSAlert()
        alert.messageText = "Connect to Server"
        alert.informativeText = "Enter the control URL of an Interseptor server, for example http://100.64.0.1:9966. The local proxy is not started while connected to a server."
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 340, height: 24))
        field.placeholderString = "http://100.64.0.1:9966"
        field.stringValue = Settings.remoteURL?.absoluteString ?? ""
        alert.accessoryView = field
        alert.addButton(withTitle: "Connect")
        alert.addButton(withTitle: "Cancel")
        if Settings.remoteURL != nil { alert.addButton(withTitle: "Use Local Proxy") }
        alert.window.initialFirstResponder = field

        let handle: (NSApplication.ModalResponse) -> Void = { response in
            switch response {
            case .alertFirstButtonReturn:
                guard let url = Settings.validatedServerURL(field.stringValue) else {
                    let bad = NSAlert()
                    bad.messageText = "Not a valid server URL"
                    bad.informativeText = "Use an http:// or https:// URL with a host, such as http://100.64.0.1:9966."
                    bad.runModal()
                    return
                }
                completion(.remote(url))
            case .alertThirdButtonReturn:
                completion(.local)
            default:
                break
            }
        }
        if let window, window.isVisible {
            alert.beginSheetModal(for: window, completionHandler: handle)
        } else {
            handle(alert.runModal())
        }
    }
}
