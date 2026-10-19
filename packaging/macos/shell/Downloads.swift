import AppKit
import WebKit

/// Saves web-view downloads (exports, blob:/data: links, attachments) through a
/// native save panel prefilled with the suggested filename.
@MainActor
final class DownloadController: NSObject, WKDownloadDelegate {
    weak var window: NSWindow?
    private var destinations: [ObjectIdentifier: URL] = [:]

    func download(_ download: WKDownload, decideDestinationUsing response: URLResponse,
                  suggestedFilename: String, completionHandler: @escaping (URL?) -> Void) {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = suggestedFilename.isEmpty ? "download" : suggestedFilename
        panel.canCreateDirectories = true
        panel.directoryURL = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask).first

        let finish: (NSApplication.ModalResponse) -> Void = { [weak self] result in
            guard result == .OK, let url = panel.url else {
                completionHandler(nil)
                return
            }
            // The panel already confirmed any overwrite; WebKit refuses to write
            // over an existing file.
            try? FileManager.default.removeItem(at: url)
            self?.destinations[ObjectIdentifier(download)] = url
            completionHandler(url)
        }
        if let window, window.isVisible {
            panel.beginSheetModal(for: window, completionHandler: finish)
        } else {
            NSApp.activate(ignoringOtherApps: true)
            finish(panel.runModal())
        }
    }

    func downloadDidFinish(_ download: WKDownload) {
        if let url = destinations.removeValue(forKey: ObjectIdentifier(download)) {
            NSWorkspace.shared.activateFileViewerSelecting([url])
        }
    }

    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
        destinations.removeValue(forKey: ObjectIdentifier(download))
        let ns = error as NSError
        if ns.domain == NSURLErrorDomain && ns.code == NSURLErrorCancelled { return }
        let alert = NSAlert(error: error)
        alert.messageText = "Download failed"
        if let window { alert.beginSheetModal(for: window) } else { alert.runModal() }
    }
}
