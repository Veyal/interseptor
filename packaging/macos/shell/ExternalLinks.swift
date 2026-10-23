import Foundation

/// Which off-origin URLs the shell may hand to another app. A page (or anything
/// it renders) can navigate or window.open to any scheme, and NSWorkspace would
/// launch whatever app claims it — zoommtg://, vscode://, … — with no click
/// from the user. Only web links and mail go out; everything else is dropped.
enum ExternalLinks {
    static let allowedSchemes: Set<String> = ["http", "https", "mailto"]

    static func canOpenExternally(_ url: URL?) -> Bool {
        guard let scheme = url?.scheme?.lowercased() else { return false }
        return allowedSchemes.contains(scheme)
    }
}
