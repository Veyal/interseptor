import AppKit

/// In-app updates from GitHub releases: check, offer, download, verify, stage,
/// and hand the bundle swap to a detached helper once the app has quit.
///
/// States (each drawn by UpdateWindowController):
///   idle → checking → available(release) → downloading → verifying →
///   preparing → ready(staged app) → [Install and Relaunch] → quit → helper.
/// Cancel, Later, Remind Me Later and Skip return to idle; any failure shows an
/// error with Download Manually…. A staged update survives Later and relaunch
/// and is offered again without a new download while it still validates.
///
/// Main-thread only; URLSession delegate callbacks hop to the main queue, and
/// every async result is dropped if a newer operation (generation) began.
@MainActor
final class Updater: NSObject, URLSessionDownloadDelegate {
    static let launchDelay: TimeInterval = 10
    static let checkInterval: TimeInterval = 24 * 60 * 60
    static let maxReleaseJSON = 2 << 20
    static let maxChecksums = 64 << 10

    private enum Key {
        static let lastCheck = "UpdateLastCheck"
        static let skipped = "UpdateSkippedVersion"
        static let automatic = "UpdateAutomaticChecks"
        static let staged = "UpdateStagedVersion"
    }

    let currentVersion: String
    let bundleID: String
    let bundleURL: URL

    private let defaults = UserDefaults.standard
    private var session: URLSession!
    private var window: UpdateWindowController?
    private var timer: Timer?
    private var generation = 0
    private var checking = false
    /// A check is in flight and the user asked for it: report its outcome.
    private var reportCheck = false
    private var inProgress = false // downloading / verifying / preparing
    private var downloadTask: URLSessionDownloadTask?
    private var release: ReleaseInfo?
    private var checksums = ""
    private var lastTick = Date.distantPast
    /// Set by Install and Relaunch; the helper starts once quitting is certain.
    private var pendingInstall: URL?

    init(currentVersion: String, bundleID: String, bundleURL: URL) {
        self.currentVersion = currentVersion
        self.bundleID = bundleID
        self.bundleURL = bundleURL
        super.init()
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 60
        config.httpAdditionalHeaders = ["User-Agent": "Interseptor/\(currentVersion)"]
        session = URLSession(configuration: config, delegate: self, delegateQueue: nil)
    }

    var automaticChecks: Bool {
        get { defaults.object(forKey: Key.automatic) as? Bool ?? true }
        set { defaults.set(newValue, forKey: Key.automatic) }
    }

    private var updatesDir: URL {
        let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
        return caches.appendingPathComponent(bundleID, isDirectory: true).appendingPathComponent("Updates", isDirectory: true)
    }

    private func versionDir(_ version: String) -> URL { updatesDir.appendingPathComponent(version, isDirectory: true) }
    private func stagedApp(_ version: String) -> URL {
        versionDir(version).appendingPathComponent("extracted/\(UpdateVerifier.appName)")
    }

    // MARK: - Scheduling

    /// Called once at launch: drops leftovers (a staged update that is no
    /// longer newer because it was installed, or an unfinished download) and
    /// schedules the automatic checks.
    func start() {
        let staged = defaults.string(forKey: Key.staged)
        if staged.map({ !Versioning.isNewer($0, than: currentVersion) }) ?? true {
            clearStaged()
        }
        // INTERSEPTOR_NO_UPDATE_CHECK=1 also silences the server's own check.
        guard ProcessInfo.processInfo.environment["INTERSEPTOR_NO_UPDATE_CHECK"] != "1" else { return }
        DispatchQueue.main.asyncAfter(deadline: .now() + Self.launchDelay) { [weak self] in
            guard let self, self.automaticChecks else { return }
            self.check(userInitiated: false)
        }
        // Hourly ticks instead of one 24h timer: a sleeping Mac does not fire
        // timers, and the tick catches up after wake.
        timer = Timer.scheduledTimer(withTimeInterval: 60 * 60, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.tick() }
        }
    }

    private func tick() {
        guard automaticChecks else { return }
        let last = defaults.object(forKey: Key.lastCheck) as? Date ?? .distantPast
        if Date().timeIntervalSince(last) >= Self.checkInterval { check(userInitiated: false) }
    }

    // MARK: - Checking

    /// Menu item, bridge message, launch and timer all land here. A
    /// user-initiated check always reports its outcome and ignores Skip.
    func check(userInitiated: Bool) {
        if inProgress || window?.isShown == true {
            if userInitiated { window?.showWindow(nil); NSApp.activate(ignoringOtherApps: true) }
            return
        }
        if userInitiated { reportCheck = true }
        guard !checking else { return }
        checking = true
        generation += 1
        let gen = generation
        var request = URLRequest(url: ReleaseInfo.latestURL)
        request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        fetch(request, limit: Self.maxReleaseJSON) { [weak self] result in
            guard let self, gen == self.generation else { return }
            self.checking = false
            let report = self.reportCheck
            self.reportCheck = false
            self.defaults.set(Date(), forKey: Key.lastCheck)
            switch result {
            case .failure(let error):
                if report { self.alert("Could not check for updates", error.localizedDescription, withReleasesPage: true) }
                else { self.offerStagedIfAny() }
            case .success(let data):
                self.handleRelease(data, report: report)
            }
        }
    }

    private func handleRelease(_ data: Data, report: Bool) {
        let info: ReleaseInfo
        do {
            info = try ReleaseInfo.parse(data)
        } catch ReleaseInfo.ParseError.notARelease {
            if report { upToDate() }
            return
        } catch ReleaseInfo.ParseError.missingAsset(let name) {
            if report { alert("This release cannot be installed automatically", "The latest release has no \(name).", withReleasesPage: true) }
            return
        } catch {
            if report { alert("Could not check for updates", "GitHub returned an unexpected response.", withReleasesPage: true) }
            return
        }
        guard Versioning.isNewer(info.version, than: currentVersion) else {
            if report { upToDate() }
            return
        }
        if !report, defaults.string(forKey: Key.skipped) == info.version { return }
        release = info
        if defaults.string(forKey: Key.staged) == info.version {
            validateStaged(info.version) { [weak self] ok in
                guard let self else { return }
                if ok { self.showReady(info.version) } else { self.clearStaged(); self.showAvailable(info) }
            }
        } else {
            showAvailable(info)
        }
    }

    /// After a failed automatic check: a previously verified update is still
    /// worth offering.
    private func offerStagedIfAny() {
        guard let staged = defaults.string(forKey: Key.staged), Versioning.isNewer(staged, than: currentVersion),
              defaults.string(forKey: Key.skipped) != staged else { return }
        validateStaged(staged) { [weak self] ok in
            if ok { self?.showReady(staged) } else { self?.clearStaged() }
        }
    }

    private func validateStaged(_ version: String, completion: @escaping (Bool) -> Void) {
        let app = stagedApp(version), id = bundleID
        let gen = generation
        DispatchQueue.global(qos: .userInitiated).async {
            let ok = (try? UpdateVerifier.validateApp(at: app, bundleID: id, version: version)) != nil
            DispatchQueue.main.async { [weak self] in
                guard let self, gen == self.generation else { return }
                completion(ok)
            }
        }
    }

    // MARK: - States

    private func ensureWindow() -> UpdateWindowController {
        if let window { return window }
        let w = UpdateWindowController()
        w.onClose = { [weak self] in self?.windowClosed() }
        window = w
        return w
    }

    private func showAvailable(_ info: ReleaseInfo) {
        let blocker = InstallBlocker.probe(bundleURL: bundleURL)
        let primary: UpdateWindowController.Button = blocker == nil
            ? .init(title: "Install Update", role: .defaultAction) { [weak self] in self?.beginInstall() }
            : .init(title: "Download Manually…", role: .defaultAction) { [weak self] in self?.openPage(info.pageURL) }
        ensureWindow().show(.init(
            headline: "A new version of Interseptor is available!",
            subtitle: "Interseptor \(info.version) is now available — you have \(currentVersion). Would you like to download it now?",
            notes: info.notes,
            warning: blocker?.explanation,
            buttons: [
                .init(title: "Skip This Version", leading: true) { [weak self] in self?.skip(info.version) },
                .init(title: "Remind Me Later", role: .cancel) { [weak self] in self?.close() },
                primary,
            ]))
    }

    private func showProgress(_ headline: String, fraction: Double? = nil, detail: String? = nil) {
        ensureWindow().show(.init(
            headline: headline,
            progress: fraction.map { .fraction($0) } ?? .indeterminate,
            detail: detail,
            buttons: [.init(title: "Cancel", role: .cancel) { [weak self] in self?.cancel() }]))
    }

    private func showReady(_ version: String) {
        let blocker = InstallBlocker.probe(bundleURL: bundleURL)
        ensureWindow().show(.init(
            headline: "Ready to Install",
            subtitle: "Interseptor \(version) has been downloaded and verified. Installing stops the proxy and relaunches Interseptor.",
            warning: blocker?.explanation,
            buttons: [
                .init(title: "Later", role: .cancel) { [weak self] in self?.close() },
                .init(title: "Install and Relaunch", role: .defaultAction) { [weak self] in self?.installAndRelaunch(version) },
            ]))
    }

    private func showFailure(_ message: String) {
        let page = release?.pageURL ?? URL(string: "https://github.com/\(ReleaseInfo.repo)/releases/latest")!
        inProgress = false
        ensureWindow().show(.init(
            headline: "Update failed",
            subtitle: message + " Interseptor was not changed.",
            buttons: [
                .init(title: "Download Manually…") { [weak self] in self?.openPage(page) },
                .init(title: "OK", role: .defaultAction) { [weak self] in self?.close() },
            ]))
    }

    private func upToDate() {
        alert("You're up to date!", "Interseptor \(currentVersion) is currently the newest version available.", withReleasesPage: false)
    }

    private func alert(_ title: String, _ message: String, withReleasesPage: Bool) {
        NSApp.activate(ignoringOtherApps: true)
        let a = NSAlert()
        a.messageText = title
        a.informativeText = message
        a.addButton(withTitle: "OK")
        if withReleasesPage { a.addButton(withTitle: "Open Releases Page") }
        if a.runModal() == .alertSecondButtonReturn {
            openPage(URL(string: "https://github.com/\(ReleaseInfo.repo)/releases/latest")!)
        }
    }

    // MARK: - Actions

    private func skip(_ version: String) {
        defaults.set(version, forKey: Key.skipped)
        close()
    }

    private func close() {
        window?.dismiss()
    }

    private func openPage(_ url: URL) {
        if ExternalLinks.canOpenExternally(url) { NSWorkspace.shared.open(url) }
    }

    private func windowClosed() {
        if inProgress { cancel() }
    }

    private func cancel() {
        generation += 1
        downloadTask?.cancel()
        downloadTask = nil
        if inProgress, let release { try? FileManager.default.removeItem(at: versionDir(release.version)) }
        inProgress = false
        close()
    }

    /// Install Update: refuse up front when the app cannot replace itself,
    /// then fetch the checksum list (small, fails fast) and the ZIP.
    private func beginInstall() {
        guard let info = release else { return }
        if let blocker = InstallBlocker.probe(bundleURL: bundleURL) {
            showFailure(blocker.explanation)
            return
        }
        generation += 1
        let gen = generation
        inProgress = true
        clearStaged()
        let dir = versionDir(info.version)
        do {
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        } catch {
            showFailure("Could not create the download folder: \(error.localizedDescription)")
            return
        }
        showProgress("Downloading update…", fraction: 0, detail: "Connecting…")
        fetch(URLRequest(url: info.checksumsURL), limit: Self.maxChecksums) { [weak self] result in
            guard let self, gen == self.generation else { return }
            switch result {
            case .failure(let error):
                self.showFailure("Could not download the release checksums: \(error.localizedDescription)")
            case .success(let data):
                let text = String(decoding: data, as: UTF8.self)
                guard Checksums.digest(for: info.zipName, in: text) != nil else {
                    self.showFailure(UpdateVerifier.Failure.checksumMissing(info.zipName).localizedDescription)
                    return
                }
                self.checksums = text
                let task = self.session.downloadTask(with: info.zipURL)
                task.taskDescription = dir.appendingPathComponent(info.zipName).path
                self.downloadTask = task
                task.resume()
            }
        }
    }

    private func downloadFinished(_ task: URLSessionDownloadTask, _ result: Result<URL, Error>) {
        guard task === downloadTask, let info = release else { return }
        downloadTask = nil
        switch result {
        case .failure(let error):
            showFailure("The download failed: \(error.localizedDescription)")
        case .success(let zip):
            verify(zip, info)
        }
    }

    /// Verifying (checksum) then Preparing (unpack, identity, version,
    /// signature), off the main thread.
    private func verify(_ zip: URL, _ info: ReleaseInfo) {
        let gen = generation, sums = checksums, id = bundleID
        let extractDir = stagedApp(info.version).deletingLastPathComponent()
        showProgress("Verifying…")
        DispatchQueue.global(qos: .userInitiated).async {
            do {
                try UpdateVerifier.checkDigest(of: zip, checksums: sums)
            } catch {
                DispatchQueue.main.async { [weak self] in
                    guard let self, gen == self.generation else { return }
                    self.showFailure(error.localizedDescription)
                }
                return
            }
            DispatchQueue.main.async { [weak self] in
                guard let self, gen == self.generation else { return }
                self.showProgress("Preparing…")
                DispatchQueue.global(qos: .userInitiated).async {
                    let outcome = Result { () -> URL in
                        let app = try UpdateVerifier.extract(zip, into: extractDir)
                        try UpdateVerifier.validateApp(at: app, bundleID: id, version: info.version)
                        return app
                    }
                    try? FileManager.default.removeItem(at: zip)
                    DispatchQueue.main.async { [weak self] in
                        guard let self, gen == self.generation else { return }
                        switch outcome {
                        case .failure(let error):
                            try? FileManager.default.removeItem(at: extractDir)
                            self.showFailure(error.localizedDescription)
                        case .success:
                            self.inProgress = false
                            self.defaults.set(info.version, forKey: Key.staged)
                            self.showReady(info.version)
                        }
                    }
                }
            }
        }
    }

    private func installAndRelaunch(_ version: String) {
        if let blocker = InstallBlocker.probe(bundleURL: bundleURL) {
            showFailure(blocker.explanation)
            return
        }
        let app = stagedApp(version)
        guard FileManager.default.fileExists(atPath: app.path) else {
            clearStaged()
            showFailure("The downloaded update is no longer available.")
            return
        }
        pendingInstall = app
        close()
        // The normal quit path: held-request confirmation, graceful server
        // stop, then applicationWillTerminate → launchPendingInstall().
        NSApp.terminate(nil)
    }

    /// The quit was cancelled (held-request prompt): do not install later.
    func cancelPendingInstall() { pendingInstall = nil }

    /// Called from applicationWillTerminate, after the server has stopped.
    func launchPendingInstall() {
        guard let app = pendingInstall else { return }
        pendingInstall = nil
        let script = updatesDir.appendingPathComponent("install-helper.sh")
        let log = UpdateHelperScript.logURL
        do {
            try UpdateHelperScript.write(to: script)
            try UpdateHelperScript.spawn(script: script, arguments: UpdateHelperScript.arguments(
                pid: getpid(), app: bundleURL, newApp: app, log: log, relaunch: true))
            defaults.removeObject(forKey: Key.staged)
        } catch {
            // Too late for UI: the app is exiting. Leave a trace in the log.
            if let h = Supervisor.appendHandle(at: log) {
                h.write(Data("\(Date()) update: could not start the install helper: \(error)\n".utf8))
                try? h.close()
            }
        }
    }

    private func clearStaged() {
        defaults.removeObject(forKey: Key.staged)
        try? FileManager.default.removeItem(at: updatesDir)
    }

    // MARK: - Networking

    /// GETs a small document (release JSON, checksum list), refusing anything
    /// but a 200 from a trusted host and anything larger than limit.
    private func fetch(_ request: URLRequest, limit: Int, completion: @escaping (Result<Data, Error>) -> Void) {
        session.dataTask(with: request) { data, response, error in
            let result: Result<Data, Error>
            if let error {
                result = .failure(error)
            } else if let http = response as? HTTPURLResponse, http.statusCode == 200,
                      UpdateHosts.isTrustedAPI(http.url) || UpdateHosts.isTrustedDownload(http.url),
                      let data, data.count <= limit {
                result = .success(data)
            } else {
                let code = (response as? HTTPURLResponse)?.statusCode ?? 0
                result = .failure(UpdateNetworkError.badResponse(code))
            }
            DispatchQueue.main.async { completion(result) }
        }.resume()
    }

    // Every redirect must stay on HTTPS GitHub hosts; anything else is refused
    // (the redirect response then fails the 200 check).
    nonisolated func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                                newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        let ok = UpdateHosts.isTrustedDownload(request.url) || UpdateHosts.isTrustedAPI(request.url)
        completionHandler(ok ? request : nil)
    }

    nonisolated func urlSession(_ session: URLSession, downloadTask: URLSessionDownloadTask, didWriteData bytesWritten: Int64,
                                totalBytesWritten: Int64, totalBytesExpectedToWrite: Int64) {
        DispatchQueue.main.async { [weak self] in
            self?.downloadProgress(downloadTask, totalBytesWritten, totalBytesExpectedToWrite)
        }
    }

    // The temporary file is deleted when this returns, so it is moved into
    // the cache here, on the delegate queue, before hopping to main.
    nonisolated func urlSession(_ session: URLSession, downloadTask: URLSessionDownloadTask, didFinishDownloadingTo location: URL) {
        let result: Result<URL, Error>
        let http = downloadTask.response as? HTTPURLResponse
        if let path = downloadTask.taskDescription, http?.statusCode == 200, UpdateHosts.isTrustedDownload(http?.url) {
            let dest = URL(fileURLWithPath: path)
            try? FileManager.default.removeItem(at: dest)
            do {
                try FileManager.default.moveItem(at: location, to: dest)
                result = .success(dest)
            } catch {
                result = .failure(error)
            }
        } else {
            result = .failure(UpdateNetworkError.badResponse(http?.statusCode ?? 0))
        }
        DispatchQueue.main.async { [weak self] in self?.downloadFinished(downloadTask, result) }
    }

    nonisolated func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        guard let error, let download = task as? URLSessionDownloadTask else { return }
        DispatchQueue.main.async { [weak self] in self?.downloadFinished(download, .failure(error)) }
    }

    private func downloadProgress(_ task: URLSessionDownloadTask, _ received: Int64, _ total: Int64) {
        guard task === downloadTask else { return }
        // Throttled: the delegate reports every few KB.
        let now = Date()
        guard now.timeIntervalSince(lastTick) >= 0.1 || received == total else { return }
        lastTick = now
        let f = ByteCountFormatter()
        f.countStyle = .file
        if total > 0 {
            window?.setProgress(Double(received) / Double(total),
                                detail: "\(f.string(fromByteCount: received)) of \(f.string(fromByteCount: total))")
        } else {
            window?.setProgress(0, detail: f.string(fromByteCount: received))
        }
    }
}

enum UpdateNetworkError: Error, LocalizedError {
    case badResponse(Int)
    var errorDescription: String? {
        switch self {
        case .badResponse(let code):
            return code == 0 ? "The server's response was refused." : "The server answered with HTTP \(code)."
        }
    }
}
