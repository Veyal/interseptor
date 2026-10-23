import Foundation

/// Saving a download over an existing file. WebKit refuses to write over an
/// existing file, but deleting the original up front loses it if the download
/// then fails. So the download goes to a hidden sibling and only replaces the
/// original once it is complete.
enum DownloadStaging {
    /// Where WebKit should write a download the user chose to save at
    /// destination: destination itself when nothing is there yet, otherwise a
    /// unique temp name in the same directory (same volume, so the final swap
    /// is a rename).
    static func stagingURL(for destination: URL, fileManager: FileManager = .default) -> URL {
        guard fileManager.fileExists(atPath: destination.path) else { return destination }
        let name = ".\(destination.lastPathComponent).\(UUID().uuidString).download"
        return destination.deletingLastPathComponent().appendingPathComponent(name)
    }

    /// Moves a finished staged download into place over the original.
    static func commit(staged: URL, to destination: URL, fileManager: FileManager = .default) throws {
        guard staged != destination else { return }
        if fileManager.fileExists(atPath: destination.path) {
            _ = try fileManager.replaceItemAt(destination, withItemAt: staged)
        } else {
            try fileManager.moveItem(at: staged, to: destination)
        }
    }

    /// Removes a staged download that failed; the original is left untouched.
    static func discard(staged: URL, destination: URL, fileManager: FileManager = .default) {
        guard staged != destination else { return }
        try? fileManager.removeItem(at: staged)
    }
}
