import Foundation

/// The UI's `{type:'status'}` bridge message.
struct DesktopStatus: Equatable {
    struct Held: Equatable {
        var method: String
        var host: String
        var path: String

        var summary: String { [method, host + path].filter { !$0.isEmpty }.joined(separator: " ") }
    }

    var project = ""
    var live = "offline"
    var interceptRequest = false
    var interceptResponse = false
    var held = 0
    var heldLatest: Held?

    init() {}

    init(message m: [String: Any]) {
        project = m["project"] as? String ?? ""
        live = m["live"] as? String ?? "offline"
        if let i = m["intercept"] as? [String: Any] {
            interceptRequest = (i["request"] as? Bool) ?? false
            interceptResponse = (i["response"] as? Bool) ?? false
        }
        held = max(0, (m["held"] as? NSNumber)?.intValue ?? 0)
        if let h = m["heldLatest"] as? [String: Any] {
            heldLatest = Held(method: h["method"] as? String ?? "",
                              host: h["host"] as? String ?? "",
                              path: h["path"] as? String ?? "")
        }
    }

    var liveLabel: String {
        switch live {
        case "connected": return "Live"
        case "reconnecting": return "Reconnecting…"
        default: return "Offline"
        }
    }
}
