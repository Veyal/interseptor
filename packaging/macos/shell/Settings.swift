import Foundation

/// User-tunable shell settings, stored in the app's UserDefaults domain so they
/// can be set with `defaults write <bundle id> <Key> <value>` even though a
/// Finder-launched app inherits launchd's environment, not a shell's.
enum Settings {
    private static var defaults: UserDefaults { .standard }

    /// Defaults keys forwarded to the supervisor (and so to the server) as env.
    static let environmentKeys: [(key: String, env: String)] = [
        ("ControlAddr", "INTERSEPTOR_CONTROL_ADDR"),
        ("ProxyAddr", "INTERSEPTOR_PROXY_ADDR"),
        ("DataDir", "INTERSEPTOR_DATA_DIR"),
        ("Project", "INTERSEPTOR_PROJECT"),
    ]

    static func supervisorEnvironment() -> [String: String] {
        var env = ProcessInfo.processInfo.environment
        for (key, name) in environmentKeys {
            if let value = defaults.string(forKey: key)?.trimmingCharacters(in: .whitespaces), !value.isEmpty {
                env[name] = value
            }
        }
        return env
    }

    /// A remote control URL (for example a Tailscale address). When set, the
    /// shell connects to it instead of starting a local proxy.
    static var remoteURL: URL? {
        get {
            guard let raw = defaults.string(forKey: "RemoteURL") else { return nil }
            return validatedServerURL(raw)
        }
        set { defaults.set(newValue?.absoluteString, forKey: "RemoteURL") }
    }

    static func validatedServerURL(_ raw: String) -> URL? {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed),
              let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https",
              let host = url.host, !host.isEmpty else { return nil }
        return url
    }

    /// Path of the UI the shell loads on the control server. "/" is the shared
    /// web UI; a desktop-specific UI can be selected later by changing this one
    /// constant, or per machine with `defaults write <bundle id> UIPath /desktop/`.
    static let defaultUIPath = "/"

    static var uiPath: String {
        var path = defaults.string(forKey: "UIPath")?.trimmingCharacters(in: .whitespaces) ?? ""
        if path.isEmpty { path = defaultUIPath }
        return path.hasPrefix("/") ? path : "/" + path
    }

    /// The page to load for a control server: its origin plus uiPath. Any path
    /// on the base URL is dropped, so a remote URL and a READY URL behave alike.
    static func uiURL(base: URL) -> URL {
        guard var c = URLComponents(url: base, resolvingAgainstBaseURL: false) else { return base }
        c.path = ""
        c.query = nil
        c.fragment = nil
        guard let origin = c.url?.absoluteString else { return base }
        return URL(string: origin + uiPath) ?? base
    }

    /// The last theme the UI reported, so the next launch paints the right
    /// background before the page has loaded.
    static var lastBackground: String? {
        get { defaults.string(forKey: "LastThemeBackground") }
        set { defaults.set(newValue, forKey: "LastThemeBackground") }
    }

    static var lastScheme: String? {
        get { defaults.string(forKey: "LastThemeScheme") }
        set { defaults.set(newValue, forKey: "LastThemeScheme") }
    }
}
