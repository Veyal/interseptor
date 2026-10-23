import CryptoKit
import Foundation

/// Checks a downloaded update before it may replace the running app. Every
/// step must pass; any failure refuses the install.
///
/// Trust model: the app is ad-hoc signed, so there is no developer identity to
/// pin. The ZIP must match the SHA-256 the same GitHub release lists in
/// macos-checksums.txt (both fetched over HTTPS from GitHub hosts), and the
/// bundle inside must be ours (same identifier), the advertised version, and
/// carry an intact code signature.
enum UpdateVerifier {
    enum Failure: Error, LocalizedError, Equatable {
        case checksumMissing(String)
        case checksumMismatch
        case unreadable(String)
        case extractFailed(String)
        case appMissing
        case wrongIdentifier(String)
        case wrongVersion(String)
        case signatureInvalid(String)

        var errorDescription: String? {
            switch self {
            case .checksumMissing(let name): return "The release's checksum file does not list \(name)."
            case .checksumMismatch: return "The downloaded update does not match the release checksum."
            case .unreadable(let why): return "The downloaded update could not be read: \(why)"
            case .extractFailed(let why): return "The update archive could not be unpacked: \(why)"
            case .appMissing: return "The update archive does not contain Interseptor.app."
            case .wrongIdentifier(let id): return "The update is a different app (\(id))."
            case .wrongVersion(let v): return "The update reports version \(v), not the version the release advertised."
            case .signatureInvalid(let why): return "The update's code signature is not valid: \(why)"
            }
        }
    }

    static let appName = "Interseptor.app"

    /// SHA-256 of the file in 1 MiB chunks, so a 60 MB+ ZIP is never held in
    /// memory at once.
    static func sha256Hex(of url: URL) throws -> String {
        guard let handle = try? FileHandle(forReadingFrom: url) else { throw Failure.unreadable(url.lastPathComponent) }
        defer { try? handle.close() }
        var hasher = SHA256()
        while true {
            let chunk = try autoreleasepool { try handle.read(upToCount: 1 << 20) ?? Data() }
            if chunk.isEmpty { break }
            hasher.update(data: chunk)
        }
        return hasher.finalize().map { String(format: "%02x", $0) }.joined()
    }

    static func checkDigest(of zip: URL, checksums: String) throws {
        guard let expected = Checksums.digest(for: zip.lastPathComponent, in: checksums) else {
            throw Failure.checksumMissing(zip.lastPathComponent)
        }
        guard try sha256Hex(of: zip) == expected else { throw Failure.checksumMismatch }
    }

    /// Unpacks zip into a fresh directory with ditto (which preserves the
    /// bundle's signature xattrs and symlinks) and returns the app inside.
    static func extract(_ zip: URL, into dir: URL) throws -> URL {
        try? FileManager.default.removeItem(at: dir)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let r = run("/usr/bin/ditto", ["-x", "-k", zip.path, dir.path])
        guard r.status == 0 else { throw Failure.extractFailed(r.output) }
        let app = dir.appendingPathComponent(appName)
        let values = try? app.resourceValues(forKeys: [.isDirectoryKey, .isSymbolicLinkKey])
        guard values?.isDirectory == true, values?.isSymbolicLink != true else { throw Failure.appMissing }
        return app
    }

    /// The bundle checks, also re-run on an update staged by an earlier run.
    static func validateApp(at app: URL, bundleID: String, version: String) throws {
        let plist = app.appendingPathComponent("Contents/Info.plist")
        guard let info = NSDictionary(contentsOf: plist) as? [String: Any] else { throw Failure.appMissing }
        let id = info["CFBundleIdentifier"] as? String ?? "?"
        guard id == bundleID else { throw Failure.wrongIdentifier(id) }
        let v = info["CFBundleShortVersionString"] as? String ?? "?"
        guard Versioning.isSame(v, version) else { throw Failure.wrongVersion(v) }
        let r = run("/usr/bin/codesign", ["--verify", "--deep", "--strict", app.path])
        guard r.status == 0 else { throw Failure.signatureInvalid(r.output) }
    }

    /// Checksum, extract, validate. Returns the verified app.
    static func verify(zip: URL, checksums: String, extractTo dir: URL,
                       bundleID: String, version: String) throws -> URL {
        try checkDigest(of: zip, checksums: checksums)
        let app = try extract(zip, into: dir)
        try validateApp(at: app, bundleID: bundleID, version: version)
        return app
    }

    /// Runs a tool and returns its status with combined output (trimmed to a
    /// readable length for error messages).
    static func run(_ path: String, _ args: [String]) -> (status: Int32, output: String) {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: path)
        p.arguments = args
        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError = pipe
        p.standardInput = FileHandle.nullDevice
        do { try p.run() } catch { return (-1, error.localizedDescription) }
        // Read before waiting so a chatty tool cannot block on a full pipe.
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        p.waitUntilExit()
        try? pipe.fileHandleForReading.close()
        let text = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        return (p.terminationStatus, String(text.prefix(400)))
    }
}
