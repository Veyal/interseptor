import AppKit
import WebKit

/// Saves web-view downloads (exports, blob:/data: links, attachments) through a
/// native save panel prefilled with the suggested filename.
@MainActor
final class DownloadController: NSObject, WKDownloadDelegate {
    weak var window: NSWindow?
    private var destinations: [ObjectIdentifier: (staged: URL, final: URL)] = [:]

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
            // The panel already confirmed any overwrite. WebKit refuses to write
            // over an existing file, so it writes a staging file that replaces
            // the original only once the download has finished.
            let staged = DownloadStaging.stagingURL(for: url)
            self?.destinations[ObjectIdentifier(download)] = (staged, url)
            completionHandler(staged)
        }
        if let window, window.isVisible {
            panel.beginSheetModal(for: window, completionHandler: finish)
        } else {
            NSApp.activate(ignoringOtherApps: true)
            finish(panel.runModal())
        }
    }

    func downloadDidFinish(_ download: WKDownload) {
        guard let dest = destinations.removeValue(forKey: ObjectIdentifier(download)) else { return }
        do {
            try DownloadStaging.commit(staged: dest.staged, to: dest.final)
            NSWorkspace.shared.activateFileViewerSelecting([dest.final])
        } catch {
            DownloadStaging.discard(staged: dest.staged, destination: dest.final)
            showFailure(error)
        }
    }

    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
        if let dest = destinations.removeValue(forKey: ObjectIdentifier(download)) {
            DownloadStaging.discard(staged: dest.staged, destination: dest.final)
        }
        let ns = error as NSError
        if ns.domain == NSURLErrorDomain && ns.code == NSURLErrorCancelled { return }
        showFailure(error)
    }

    private func showFailure(_ error: Error) {
        let alert = NSAlert(error: error)
        alert.messageText = "Download failed"
        if let window { alert.beginSheetModal(for: window) } else { alert.runModal() }
    }
}
