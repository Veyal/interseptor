import Foundation

/// Release versions as the updater sees them. Tags are "v2.18.1"; bundle
/// versions are plain numeric-dotted ("2.18.1"). Anything with a prerelease
/// suffix ("2.19.0-rc.1") is never offered as an update.
enum Versioning {
    /// The numeric components of a release version, or nil when it is not a
    /// plain release: a leading "v" and "+build" metadata are ignored, a
    /// "-prerelease" suffix or any non-numeric part rejects it.
    static func components(_ raw: String) -> [Int]? {
        var s = raw.trimmingCharacters(in: .whitespaces)
        if s.hasPrefix("v") || s.hasPrefix("V") { s.removeFirst() }
        if let plus = s.firstIndex(of: "+") { s = String(s[..<plus]) }
        guard !s.isEmpty, !s.contains("-") else { return nil }
        var parts: [Int] = []
        for piece in s.split(separator: ".", omittingEmptySubsequences: false) {
            guard !piece.isEmpty, piece.allSatisfy(\.isASCII), piece.allSatisfy(\.isNumber),
                  let n = Int(piece) else { return nil }
            parts.append(n)
        }
        return parts
    }

    /// "v2.18.1" → "2.18.1"; nil for a prerelease or malformed tag.
    static func releaseVersion(fromTag tag: String) -> String? {
        components(tag).map { $0.map(String.init).joined(separator: ".") }
    }

    /// Numeric comparison with missing components read as 0 (2.18 == 2.18.0,
    /// 2.10.0 > 2.9.0). nil when either side is not a plain release.
    static func compare(_ a: String, _ b: String) -> ComparisonResult? {
        guard let x = components(a), let y = components(b) else { return nil }
        for i in 0..<max(x.count, y.count) {
            let l = i < x.count ? x[i] : 0, r = i < y.count ? y[i] : 0
            if l != r { return l < r ? .orderedAscending : .orderedDescending }
        }
        return .orderedSame
    }

    static func isNewer(_ candidate: String, than current: String) -> Bool {
        compare(candidate, current) == .orderedDescending
    }

    static func isSame(_ a: String, _ b: String) -> Bool {
        compare(a, b) == .orderedSame
    }
}

/// The parts of a GitHub release the updater needs, validated up front so a
/// half-usable release is rejected at check time rather than mid-install.
struct ReleaseInfo: Equatable {
    static let repo = "Veyal/interseptor"
    static let latestURL = URL(string: "https://api.github.com/repos/\(repo)/releases/latest")!
    static let checksumsName = "macos-checksums.txt"

    let tag: String
    /// Numeric-dotted, no "v".
    let version: String
    let notes: String
    let pageURL: URL
    let zipName: String
    let zipURL: URL
    let checksumsURL: URL

    enum ParseError: Error, Equatable {
        case malformed
        /// A draft, a prerelease, or a tag that is not a plain version.
        case notARelease
        case missingAsset(String)
        case untrustedURL(String)
    }

    /// The app ZIP name release.yml publishes for a tag.
    static func zipName(forTag tag: String) -> String { "Interseptor-\(tag)-unsigned.zip" }

    static func releasesPage(tag: String) -> URL {
        let escaped = tag.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? ""
        return URL(string: "https://github.com/\(repo)/releases/tag/\(escaped)")
            ?? URL(string: "https://github.com/\(repo)/releases")!
    }

    /// Parses the body of GET /repos/{repo}/releases/latest.
    static func parse(_ data: Data) throws -> ReleaseInfo {
        guard let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let tag = obj["tag_name"] as? String else { throw ParseError.malformed }
        if obj["draft"] as? Bool == true || obj["prerelease"] as? Bool == true { throw ParseError.notARelease }
        guard let version = Versioning.releaseVersion(fromTag: tag) else { throw ParseError.notARelease }
        let assets = (obj["assets"] as? [[String: Any]]) ?? []
        func asset(_ name: String) throws -> URL {
            guard let entry = assets.first(where: { $0["name"] as? String == name }),
                  let raw = entry["browser_download_url"] as? String else { throw ParseError.missingAsset(name) }
            guard let url = URL(string: raw), UpdateHosts.isTrustedDownload(url) else { throw ParseError.untrustedURL(raw) }
            return url
        }
        let zip = zipName(forTag: tag)
        let page = (obj["html_url"] as? String).flatMap(URL.init(string:)).flatMap { UpdateHosts.isTrustedDownload($0) ? $0 : nil }
        return ReleaseInfo(tag: tag, version: version, notes: obj["body"] as? String ?? "",
                           pageURL: page ?? releasesPage(tag: tag),
                           zipName: zip, zipURL: try asset(zip), checksumsURL: try asset(checksumsName))
    }
}

/// Where update bytes may come from: GitHub's release download host and the
/// CDN hosts it redirects to, over HTTPS only. Checked for every asset URL,
/// every redirect, and the final response URL.
enum UpdateHosts {
    static let downloadHosts: Set<String> = [
        "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com",
    ]

    static func isTrustedDownload(_ url: URL?) -> Bool {
        guard let url, url.scheme?.lowercased() == "https", let host = url.host?.lowercased(),
              url.user == nil, url.password == nil else { return false }
        return downloadHosts.contains(host)
    }

    /// The API host is only ever reached through ReleaseInfo.latestURL.
    static func isTrustedAPI(_ url: URL?) -> Bool {
        url?.scheme?.lowercased() == "https" && url?.host?.lowercased() == "api.github.com"
    }
}

/// sha256sum-format checksum files: "<64 hex>  <name>" (or " *<name>" for
/// binary mode), one per line.
enum Checksums {
    /// The lowercase digest listed for exactly name, or nil. A line for
    /// "x-Interseptor-v1-unsigned.zip" must never satisfy "Interseptor-v1-unsigned.zip".
    static func digest(for name: String, in text: String) -> String? {
        for line in text.split(whereSeparator: \.isNewline) {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            guard let space = trimmed.firstIndex(where: { $0 == " " || $0 == "\t" }) else { continue }
            let hex = trimmed[..<space].lowercased()
            var file = trimmed[space...].drop(while: { $0 == " " || $0 == "\t" })
            if file.hasPrefix("*") { file = file.dropFirst() }
            guard file == name, isSHA256Hex(hex) else { continue }
            return hex
        }
        return nil
    }

    static func isSHA256Hex(_ s: String) -> Bool {
        s.count == 64 && s.allSatisfy { $0.isHexDigit && $0.isASCII }
    }
}

/// Why the running copy cannot replace itself. Detected before any download so
/// the user gets an explanation instead of a late failure.
enum InstallBlocker: Equatable {
    /// Gatekeeper App Translocation runs a quarantined app from a random
    /// read-only mount; the real location is unknown to us.
    case translocated
    /// Running straight from the mounted DMG (or another read-only volume).
    case readOnlyVolume
    /// The folder holding the app is not writable by this user.
    case parentNotWritable
    /// Not running from an .app bundle (a development build).
    case notAnAppBundle

    var explanation: String {
        switch self {
        case .translocated:
            return "macOS is running Interseptor from a temporary location, so it cannot update itself. Move Interseptor to your Applications folder, open it from there, and check again."
        case .readOnlyVolume:
            return "Interseptor is running from a read-only volume (probably the disk image). Drag it to your Applications folder, open it from there, and check again."
        case .parentNotWritable:
            return "You don't have permission to change the folder Interseptor is in. Move it to a folder you can write to, such as Applications, or download the update manually."
        case .notAnAppBundle:
            return "This copy of Interseptor is not running from an app bundle, so it cannot update itself."
        }
    }

    /// The decision, from facts gathered by probe(bundleURL:).
    static func decide(bundlePath: String, volumeIsReadOnly: Bool, parentIsWritable: Bool) -> InstallBlocker? {
        if !bundlePath.hasSuffix(".app") && !bundlePath.hasSuffix(".app/") { return .notAnAppBundle }
        if bundlePath.contains("/AppTranslocation/") { return .translocated }
        if volumeIsReadOnly { return .readOnlyVolume }
        if !parentIsWritable { return .parentNotWritable }
        return nil
    }

    static func probe(bundleURL: URL, fileManager: FileManager = .default) -> InstallBlocker? {
        let readOnly = (try? bundleURL.resourceValues(forKeys: [.volumeIsReadOnlyKey]))?.volumeIsReadOnly ?? false
        let parent = bundleURL.deletingLastPathComponent().path
        return decide(bundlePath: bundleURL.path, volumeIsReadOnly: readOnly,
                      parentIsWritable: fileManager.isWritableFile(atPath: parent))
    }
}

/// GitHub release notes are full Markdown; AppKit renders only inline
/// Markdown, so block syntax is turned into plain-text equivalents first.
enum ReleaseNotes {
    static func inlineMarkdown(_ body: String) -> String {
        body.replacingOccurrences(of: "\r\n", with: "\n")
            .split(separator: "\n", omittingEmptySubsequences: false)
            .map { line -> String in
                let indent = line.prefix(while: { $0 == " " })
                let rest = line.dropFirst(indent.count)
                if rest.hasPrefix("#") {
                    let text = rest.drop(while: { $0 == "#" }).trimmingCharacters(in: .whitespaces)
                    return text.isEmpty ? "" : "**\(text)**"
                }
                if rest.hasPrefix("- ") || rest.hasPrefix("* ") || rest.hasPrefix("+ ") {
                    return indent + "• " + rest.dropFirst(2)
                }
                return String(line)
            }
            .joined(separator: "\n")
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }
}
