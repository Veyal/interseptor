import Foundation

// Supervisor state-machine tests, compiled with plain swiftc together with
// shell/Supervisor.swift (no Xcode needed). Run by TestSwiftSupervisor in
// cmd/interseptor-macapp, or: packaging/macos/shell-tests/run.sh

var failures = 0
func check(_ cond: Bool, _ msg: String) {
    if cond { print("ok   \(msg)") } else { print("FAIL \(msg)"); failures += 1 }
}

/// Spins the main run loop until cond holds or timeout passes.
@discardableResult
func spin(until cond: () -> Bool, timeout: TimeInterval) -> Bool {
    let end = Date().addingTimeInterval(timeout)
    while !cond() && Date() < end { RunLoop.main.run(until: Date().addingTimeInterval(0.02)) }
    return cond()
}

let dir = FileManager.default.temporaryDirectory.appendingPathComponent("sup-tests-\(getpid())")
try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
defer { try? FileManager.default.removeItem(at: dir) }

func script(_ name: String, _ body: String) -> URL {
    let url = dir.appendingPathComponent(name)
    try! ("#!/bin/bash\n" + body + "\n").write(to: url, atomically: true, encoding: .utf8)
    try! FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: url.path)
    return url
}

// Behaves like the real supervisor: READY, then waits; exits on SIGTERM.
let polite = script("polite.sh", """
trap 'exit 0' TERM
echo "INTERSEPTOR_READY http://127.0.0.1:1"
while true; do sleep 0.1; done
""")
// Ignores SIGTERM, so only SIGKILL stops it.
let stubborn = script("stubborn.sh", """
trap '' TERM
echo "INTERSEPTOR_READY http://127.0.0.1:2"
while true; do sleep 0.1; done
""")
let crasher = script("crash.sh", "exit 3")
let env = ProcessInfo.processInfo.environment

// 1. parse
if case .ready(let u)? = Supervisor.parse("INTERSEPTOR_READY http://127.0.0.1:9\n") { check(u.port == 9, "parse READY") }
else { check(false, "parse READY") }
if case .error(let m)? = Supervisor.parse("INTERSEPTOR_ERROR boom") { check(m == "boom", "parse ERROR") }
else { check(false, "parse ERROR") }
check(Supervisor.parse("noise") == nil, "parse ignores other lines")

// 2. clean start / stop: completion only after the process exited
do {
    let s = Supervisor()
    var events: [String] = []
    s.onEvent = { if case .ready(let u) = $0 { events.append("ready:\(u.port ?? 0)") } else { events.append("other") } }
    try s.start(executable: polite, environment: env, arguments: [])
    check(spin(until: { !events.isEmpty }, timeout: 5), "polite: READY delivered")
    check(s.isRunning, "polite: running")
    var done = false
    var runningAtCompletion = true
    s.stop(timeout: 3) { done = true; runningAtCompletion = s.isRunning }
    check(spin(until: { done }, timeout: 5), "polite: stop completes")
    check(!runningAtCompletion, "polite: not running when completion fires")
    check(events == ["ready:1"], "polite: no spurious events (\(events))")
}

// 3. unexpected exit is reported
do {
    let s = Supervisor()
    var code: Int32?
    s.onEvent = { if case .exited(let c) = $0 { code = c } }
    try s.start(executable: crasher, environment: env, arguments: [])
    check(spin(until: { code != nil }, timeout: 5), "crash: exit reported")
    check(code == 3, "crash: status 3 (\(String(describing: code)))")
    check(!s.isRunning, "crash: not running")
}

// 4. stop timeout on a SIGTERM-ignoring child, then restart from the completion
do {
    let s = Supervisor()
    var events: [String] = []
    s.onEvent = {
        switch $0 {
        case .ready(let u): events.append("ready:\(u.port ?? 0)")
        case .exited: events.append("exited")
        case .error: events.append("error")
        }
    }
    try s.start(executable: stubborn, environment: env, arguments: [])
    check(spin(until: { events == ["ready:2"] }, timeout: 5), "stubborn: READY delivered")
    let t0 = Date()
    var completed = false
    var runningAtCompletion = true
    var restarted = false
    s.stop(timeout: 0.6) {
        completed = true
        runningAtCompletion = s.isRunning
        // What AppDelegate.restart does: connect() bails if still running.
        if !s.isRunning { try? s.start(executable: polite, environment: env, arguments: []); restarted = true }
    }
    check(spin(until: { completed }, timeout: 0.4) == false, "stubborn: completion NOT fired before the timeout")
    check(spin(until: { completed }, timeout: 6), "stubborn: completion fires after SIGKILL escalation")
    check(Date().timeIntervalSince(t0) >= 0.55, "stubborn: waited for the timeout")
    check(!runningAtCompletion, "stubborn: old process dead when completion fires")
    check(restarted, "stubborn: restart proceeded")
    check(spin(until: { events.contains("ready:1") }, timeout: 5), "restart: new READY delivered")
    // The old process's late exit must not disturb the new one.
    _ = spin(until: { false }, timeout: 1.0)
    check(s.isRunning, "restart: new process still running after stale exit")
    check(!events.contains("exited"), "restart: no spurious .exited (\(events))")
    var done = false
    s.stop(timeout: 3) { done = true }
    check(spin(until: { done }, timeout: 5), "restart: new process stops cleanly")
    check(!s.isRunning, "restart: stopped")
}

// 5. concurrent stops share one termination; both completions run
do {
    let s = Supervisor()
    try s.start(executable: polite, environment: env, arguments: [])
    _ = spin(until: { false }, timeout: 0.3)
    var n = 0
    s.stop(timeout: 3) { n += 1 }
    s.stop(timeout: 3) { n += 1 }
    check(spin(until: { n == 2 }, timeout: 5), "double stop: both completions run")
}

// 6. stop with nothing running completes at once
do {
    let s = Supervisor()
    var done = false
    s.stop(timeout: 1) { done = true }
    check(done, "idle stop: immediate completion")
}

// 7. no file descriptors leak across runs (stdin pipe, stdout reader, log fd)
do {
    func openFDs() -> Int { (try? FileManager.default.contentsOfDirectory(atPath: "/dev/fd").count) ?? -1 }
    let s = Supervisor()
    var exits = 0
    s.onEvent = { if case .exited = $0 { exits += 1 } }
    // One warm-up run so lazily opened system fds are not counted as leaks.
    try s.start(executable: crasher, environment: env, arguments: [])
    _ = spin(until: { exits == 1 }, timeout: 5)
    _ = spin(until: { false }, timeout: 0.2)
    let baseline = openFDs()
    for i in 2...11 {
        try s.start(executable: crasher, environment: env, arguments: [])
        _ = spin(until: { exits == i }, timeout: 5)
    }
    check(exits == 11, "fd leak: all runs exited (\(exits))")
    check(spin(until: { openFDs() <= baseline }, timeout: 3), "fd leak: fds back to baseline (\(baseline) -> \(openFDs()))")
}

// 8. an ERROR line is delivered even when the exit reaches the main queue first.
// The real race (Go prints the line, then exits) is timing-dependent; here a
// background writer that outlives the process forces the late ordering.
let lateError = script("late-error.sh", "(sleep 0.3; echo 'INTERSEPTOR_ERROR port already in use') &\nexit 1")
do {
    let s = Supervisor()
    var events: [String] = []
    s.onEvent = {
        switch $0 {
        case .error(let m): events.append("error:\(m)")
        case .exited(let c): events.append("exited:\(c)")
        case .ready: events.append("ready")
        }
    }
    try s.start(executable: lateError, environment: env, arguments: [])
    check(spin(until: { events.count == 2 }, timeout: 5), "late error: delivered after the exit (\(events))")
    check(events == ["exited:1", "error:port already in use"], "late error: exit then error (\(events))")
}

// 8b. ...but not once a newer run has started: it would explain the wrong run
do {
    let s = Supervisor()
    var events: [String] = []
    s.onEvent = {
        switch $0 {
        case .error: events.append("error")
        case .exited: events.append("exited")
        case .ready: events.append("ready")
        }
    }
    try s.start(executable: lateError, environment: env, arguments: [])
    check(spin(until: { events == ["exited"] }, timeout: 5), "late error, restarted: exit reported")
    try s.start(executable: polite, environment: env, arguments: [])
    _ = spin(until: { false }, timeout: 0.8)
    check(events == ["exited", "ready"], "late error, restarted: stale error dropped (\(events))")
    var done = false
    s.stop(timeout: 3) { done = true }
    _ = spin(until: { done }, timeout: 5)
}

// 9. a requested stop reports neither an error nor an exit (the late-ERROR
// delivery in 8 applies only to unexpected exits)
do {
    let s = Supervisor()
    var events: [String] = []
    s.onEvent = {
        switch $0 {
        case .ready(let u): events.append("ready:\(u.port ?? 0)")
        case .error: events.append("error")
        case .exited: events.append("exited")
        }
    }
    try s.start(executable: polite, environment: env, arguments: [])
    check(spin(until: { events == ["ready:1"] }, timeout: 5), "stale error: READY delivered")
    var done = false
    s.stop(timeout: 3) { done = true }
    check(spin(until: { done }, timeout: 5), "stale error: stopped")
    check(!events.contains("error") && !events.contains("exited"), "stale error: requested stop is silent (\(events))")
}

// 10. the log handle appends: another O_APPEND writer's lines survive
do {
    let log = dir.appendingPathComponent("append.log")
    try "A\n".write(to: log, atomically: true, encoding: .utf8)
    if let h = Supervisor.appendHandle(at: log), let other = FileHandle(forUpdatingAtPath: log.path) {
        other.seekToEndOfFile()
        other.write("B\n".data(using: .utf8)!) // the Go side, already at the end
        h.write("C\n".data(using: .utf8)!)
        try? h.close()
        try? other.close()
        let text = (try? String(contentsOf: log, encoding: .utf8)) ?? ""
        check(text == "A\nB\nC\n", "log append: no line overwritten (\(text.debugDescription))")
    } else {
        check(false, "log append: handles opened")
    }
}

// 11. only web and mail links leave the app
check(ExternalLinks.canOpenExternally(URL(string: "https://example.com/")), "external: https")
check(ExternalLinks.canOpenExternally(URL(string: "HTTP://example.com/")), "external: http (any case)")
check(ExternalLinks.canOpenExternally(URL(string: "mailto:a@example.com")), "external: mailto")
for raw in ["zoommtg://zoom.us/join?confno=1", "vscode://file/etc/passwd", "file:///etc/passwd",
            "javascript:alert(1)", "smb://example.com/share", "about:blank"] {
    check(!ExternalLinks.canOpenExternally(URL(string: raw)), "external: blocks \(raw)")
}
check(!ExternalLinks.canOpenExternally(nil), "external: blocks nil")

// 12. a download over an existing file keeps the original until it completes
do {
    let target = dir.appendingPathComponent("export.har")
    check(DownloadStaging.stagingURL(for: target) == target, "staging: new file downloads in place")
    try "original".write(to: target, atomically: true, encoding: .utf8)
    let staged = DownloadStaging.stagingURL(for: target)
    check(staged != target && staged.deletingLastPathComponent() == target.deletingLastPathComponent(),
          "staging: existing file gets a sibling temp")
    try "partial".write(to: staged, atomically: true, encoding: .utf8)
    DownloadStaging.discard(staged: staged, destination: target)
    check(!FileManager.default.fileExists(atPath: staged.path), "staging: failed temp removed")
    check((try? String(contentsOf: target, encoding: .utf8)) == "original", "staging: original kept on failure")
    let staged2 = DownloadStaging.stagingURL(for: target)
    try "new".write(to: staged2, atomically: true, encoding: .utf8)
    try DownloadStaging.commit(staged: staged2, to: target)
    check((try? String(contentsOf: target, encoding: .utf8)) == "new", "staging: finished download replaces original")
    check(!FileManager.default.fileExists(atPath: staged2.path), "staging: temp gone after commit")
}

// 13. automatic crash reloads are bounded
do {
    var b = ReloadBudget(limit: 3, window: 30)
    let t0 = Date()
    check(b.allow(at: t0) && b.allow(at: t0 + 1) && b.allow(at: t0 + 2), "reload budget: first 3 allowed")
    check(!b.allow(at: t0 + 3), "reload budget: 4th within the window refused")
    check(b.allow(at: t0 + 31), "reload budget: allowed again once old reloads age out")
    b.reset()
    check(b.allow(at: t0 + 32) && b.allow(at: t0 + 32) && b.allow(at: t0 + 32), "reload budget: reset restores it")
}

// 14. update versions: "v" prefix, numeric order, prereleases never offered
check(Versioning.releaseVersion(fromTag: "v2.18.1") == "2.18.1", "version: v prefix stripped")
check(Versioning.isNewer("2.10.0", than: "2.9.0"), "version: 2.10.0 > 2.9.0")
check(!Versioning.isNewer("2.9.9", than: "2.10"), "version: 2.9.9 < 2.10")
check(Versioning.isNewer("v3", than: "2.99.99"), "version: v3 > 2.99.99")
check(Versioning.isSame("2.18", "2.18.0"), "version: 2.18 == 2.18.0")
check(!Versioning.isNewer("2.18.1", than: "2.18.1"), "version: same is not newer")
check(Versioning.releaseVersion(fromTag: "v2.19.0-rc.1") == nil, "version: prerelease tag rejected")
check(Versioning.releaseVersion(fromTag: "v2.19.0+build.5") == "2.19.0", "version: build metadata ignored")
for bad in ["", "v", "2..1", "2.x", "dev", "2.1.", "٢.١"] {
    check(Versioning.components(bad) == nil, "version: rejects \(bad.debugDescription)")
}
check(!Versioning.isNewer("2.19.0-rc.1", than: "2.18.0"), "version: prerelease never newer")

// 15. release JSON → validated assets
func releaseJSON(tag: String = "v2.18.1", prerelease: Bool = false, draft: Bool = false,
                 assets: [(String, String)]? = nil) -> Data {
    let list = assets ?? [
        ("Interseptor-\(tag)-unsigned.zip.sig", "https://github.com/Veyal/interseptor/releases/download/\(tag)/x.sig"),
        ("Interseptor-\(tag)-unsigned.zip", "https://github.com/Veyal/interseptor/releases/download/\(tag)/Interseptor-\(tag)-unsigned.zip"),
        ("Interseptor_2.18.1.dmg", "https://github.com/Veyal/interseptor/releases/download/\(tag)/Interseptor_2.18.1.dmg"),
        ("macos-checksums.txt", "https://github.com/Veyal/interseptor/releases/download/\(tag)/macos-checksums.txt"),
    ]
    let obj: [String: Any] = [
        "tag_name": tag, "prerelease": prerelease, "draft": draft, "body": "## Fixed\n- a **bug**",
        "html_url": "https://github.com/Veyal/interseptor/releases/tag/\(tag)",
        "assets": list.map { ["name": $0.0, "browser_download_url": $0.1] },
    ]
    return try! JSONSerialization.data(withJSONObject: obj)
}
func parseError(_ data: Data) -> ReleaseInfo.ParseError? {
    do { _ = try ReleaseInfo.parse(data); return nil } catch { return error as? ReleaseInfo.ParseError }
}
do {
    let r = try ReleaseInfo.parse(releaseJSON())
    check(r.version == "2.18.1" && r.tag == "v2.18.1", "release: version from tag")
    check(r.zipName == "Interseptor-v2.18.1-unsigned.zip" && r.zipURL.lastPathComponent == r.zipName,
          "release: exact zip asset chosen, not the .sig (\(r.zipURL.lastPathComponent))")
    check(r.checksumsURL.lastPathComponent == "macos-checksums.txt", "release: checksum asset")
    check(r.pageURL.absoluteString == "https://github.com/Veyal/interseptor/releases/tag/v2.18.1", "release: page URL")
    check(r.notes.contains("**bug**"), "release: notes kept")
} catch {
    check(false, "release: valid JSON parses (\(error))")
}
check(parseError(releaseJSON(prerelease: true)) == .notARelease, "release: prerelease flag ignored")
check(parseError(releaseJSON(draft: true)) == .notARelease, "release: draft ignored")
check(parseError(releaseJSON(tag: "v2.19.0-beta.2")) == .notARelease, "release: prerelease tag ignored")
check(parseError(Data("not json".utf8)) == .malformed, "release: malformed body")
check(parseError(releaseJSON(assets: [("macos-checksums.txt", "https://github.com/a/b/macos-checksums.txt")]))
      == .missingAsset("Interseptor-v2.18.1-unsigned.zip"), "release: missing zip asset")
check(parseError(releaseJSON(assets: [("Interseptor-v2.18.1-unsigned.zip", "https://github.com/a/b/z.zip")]))
      == .missingAsset("macos-checksums.txt"), "release: missing checksum asset")
check(parseError(releaseJSON(assets: [("Interseptor-v2.18.1-unsigned.zip", "http://github.com/a/b/z.zip"),
                                     ("macos-checksums.txt", "https://github.com/a/b/c.txt")]))
      == .untrustedURL("http://github.com/a/b/z.zip"), "release: http asset refused")
check(parseError(releaseJSON(assets: [("Interseptor-v2.18.1-unsigned.zip", "https://evil.example.com/z.zip"),
                                     ("macos-checksums.txt", "https://github.com/a/b/c.txt")]))
      == .untrustedURL("https://evil.example.com/z.zip"), "release: foreign host refused")

// 16. download host allowlist
for ok in ["https://github.com/Veyal/interseptor/releases/download/v1/a.zip",
           "https://objects.githubusercontent.com/x", "https://release-assets.githubusercontent.com/x",
           "HTTPS://GitHub.com/x"] {
    check(UpdateHosts.isTrustedDownload(URL(string: ok)), "hosts: allows \(ok)")
}
for bad in ["http://github.com/x", "https://github.com.evil.example/x", "https://evilgithub.com/x",
            "https://user:pw@github.com/x", "https://api.github.com/x", "file:///tmp/x", "ftp://github.com/x"] {
    check(!UpdateHosts.isTrustedDownload(URL(string: bad)), "hosts: refuses \(bad)")
}
check(UpdateHosts.isTrustedAPI(ReleaseInfo.latestURL), "hosts: API URL trusted as API")
check(!UpdateHosts.isTrustedAPI(URL(string: "http://api.github.com/x")), "hosts: plain-http API refused")

// 17. checksum file parsing: exact name only
do {
    let a = String(repeating: "a", count: 64), b = String(repeating: "B", count: 64), c = String(repeating: "c", count: 64)
    let text = """
    \(a)  x-Interseptor-v2.18.1-unsigned.zip
    \(c)  Interseptor-v2.18.1-unsigned.zip.bak
    \(b) *Interseptor-v2.18.1-unsigned.zip
    0123  Interseptor_2.18.1.dmg

    """
    check(Checksums.digest(for: "Interseptor-v2.18.1-unsigned.zip", in: text) == b.lowercased(),
          "checksums: exact name (binary-mode '*'), lowercased, no suffix/prefix collision")
    check(Checksums.digest(for: "Interseptor_2.18.1.dmg", in: text) == nil, "checksums: short digest rejected")
    check(Checksums.digest(for: "Interseptor-v9-unsigned.zip", in: text) == nil, "checksums: missing line")
    check(Checksums.digest(for: "unsigned.zip", in: text) == nil, "checksums: suffix of a name never matches")
    check(Checksums.digest(for: "Interseptor-v2.18.1-unsigned.zip", in: "\(a)\tInterseptor-v2.18.1-unsigned.zip\r\n") == a,
          "checksums: tab separator and CRLF")
}

// 18. can the app replace itself?
check(InstallBlocker.decide(bundlePath: "/Applications/Interseptor.app", volumeIsReadOnly: false, parentIsWritable: true) == nil,
      "in place: /Applications ok")
check(InstallBlocker.decide(bundlePath: "/private/var/folders/x/T/AppTranslocation/ABC/d/Interseptor.app",
                            volumeIsReadOnly: true, parentIsWritable: false) == .translocated, "in place: translocation refused")
check(InstallBlocker.decide(bundlePath: "/Volumes/Interseptor/Interseptor.app", volumeIsReadOnly: true, parentIsWritable: false)
      == .readOnlyVolume, "in place: read-only volume (DMG) refused")
check(InstallBlocker.decide(bundlePath: "/Applications/Interseptor.app", volumeIsReadOnly: false, parentIsWritable: false)
      == .parentNotWritable, "in place: unwritable parent refused")
check(InstallBlocker.decide(bundlePath: "/usr/local/bin", volumeIsReadOnly: false, parentIsWritable: true)
      == .notAnAppBundle, "in place: not a bundle refused")
do {
    let parent = dir.appendingPathComponent("probe dir")
    let app = parent.appendingPathComponent("Interseptor.app")
    try FileManager.default.createDirectory(at: app, withIntermediateDirectories: true)
    check(InstallBlocker.probe(bundleURL: app) == nil, "in place: probe on a writable folder")
    try FileManager.default.setAttributes([.posixPermissions: 0o555], ofItemAtPath: parent.path)
    check(InstallBlocker.probe(bundleURL: app) == .parentNotWritable, "in place: probe on a read-only folder")
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: parent.path)
}

// 19. release notes: block markdown flattened for inline rendering
check(ReleaseNotes.inlineMarkdown("## What's new\r\n- one\n  * two\nplain") == "**What's new**\n• one\n  • two\nplain",
      "notes: headings bold, bullets as •")
check(ReleaseNotes.inlineMarkdown("  \n") == "", "notes: blank stays blank")

// 20. the install helper: paths with spaces, quotes and $ go through argv
func waitForFile(_ url: URL, containing needle: String, timeout: TimeInterval) -> Bool {
    spin(until: { ((try? String(contentsOf: url, encoding: .utf8)) ?? "").contains(needle) }, timeout: timeout)
}
func fakeApp(_ url: URL, marker: String) {
    try! FileManager.default.createDirectory(at: url.appendingPathComponent("Contents"), withIntermediateDirectories: true)
    try! marker.write(to: url.appendingPathComponent("Contents/marker"), atomically: true, encoding: .utf8)
}
func marker(_ app: URL) -> String? { try? String(contentsOf: app.appendingPathComponent("Contents/marker"), encoding: .utf8) }
do {
    let sh = UpdateVerifier.run("/bin/sh", ["-n", "-c", UpdateHelperScript.source])
    check(sh.status == 0, "helper: script parses (\(sh.output))")

    let root = dir.appendingPathComponent("it's a \"quoted\" $HOME `dir`")
    let appDir = root.appendingPathComponent("Apps with space")
    let app = appDir.appendingPathComponent("Inter septor.app")
    let cache = root.appendingPathComponent("Caches $(touch pwned)")
    let newApp = cache.appendingPathComponent("extracted/Interseptor.app")
    fakeApp(app, marker: "old")
    fakeApp(newApp, marker: "new")
    let log = root.appendingPathComponent("update.log")
    let script = cache.appendingPathComponent("install-helper.sh")
    try UpdateHelperScript.write(to: script)
    let mode = (try? FileManager.default.attributesOfItem(atPath: script.path)[.posixPermissions] as? Int) ?? 0
    check(mode == 0o700, "helper: script is owner-only (\(String(mode, radix: 8)))")

    // The "app" is a sleep that outlives the helper's start: nothing may move
    // until it exits.
    let waiter = Process()
    waiter.executableURL = URL(fileURLWithPath: "/bin/sleep")
    waiter.arguments = ["1"]
    try waiter.run()
    try UpdateHelperScript.spawn(script: script, arguments: UpdateHelperScript.arguments(
        pid: waiter.processIdentifier, app: app, newApp: newApp, log: log, relaunch: false))
    _ = spin(until: { false }, timeout: 0.5)
    check(marker(app) == "old", "helper: waits for the app to exit")
    check(waitForFile(log, containing: "installed", timeout: 10), "helper: installs after exit")
    waiter.waitUntilExit()
    check(marker(app) == "new", "helper: new bundle at the original path")
    check(!FileManager.default.fileExists(atPath: newApp.path), "helper: staged bundle moved, not copied")
    let leftovers = (try? FileManager.default.contentsOfDirectory(atPath: appDir.path)) ?? []
    check(leftovers == ["Inter septor.app"], "helper: old bundle removed (\(leftovers))")
    check(!FileManager.default.fileExists(atPath: cache.appendingPathComponent("pwned").path)
          && !FileManager.default.fileExists(atPath: dir.appendingPathComponent("pwned").path),
          "helper: no command substitution from paths")

    // Failure after the old bundle was moved aside: it is restored.
    let broken = cache.appendingPathComponent("broken/Interseptor.app")
    fakeApp(broken, marker: "broken")
    let brokenParent = broken.deletingLastPathComponent()
    try FileManager.default.setAttributes([.posixPermissions: 0o000], ofItemAtPath: broken.path)
    try FileManager.default.setAttributes([.posixPermissions: 0o555], ofItemAtPath: brokenParent.path)
    try UpdateHelperScript.spawn(script: script, arguments: UpdateHelperScript.arguments(
        pid: 999_999, app: app, newApp: broken, log: log, relaunch: false))
    check(waitForFile(log, containing: "restoring the previous version", timeout: 10), "helper: failed install reported")
    _ = spin(until: { marker(app) == "new" }, timeout: 3)
    check(marker(app) == "new", "helper: previous bundle restored after a failed install")
    let after = (try? FileManager.default.contentsOfDirectory(atPath: appDir.path)) ?? []
    check(after == ["Inter septor.app"], "helper: no backup left after restore (\(after))")
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: brokenParent.path)
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: broken.path)

    // Refuses a target that is not an .app bundle, and a non-numeric pid,
    // before touching anything.
    try UpdateHelperScript.spawn(script: script, arguments: ["1", "/", newApp.path, log.path, "0"])
    check(waitForFile(log, containing: "refusing app path '/'", timeout: 10), "helper: refuses a non-.app target")
    try UpdateHelperScript.spawn(script: script, arguments: ["1; rm -rf /", app.path, newApp.path, log.path, "0"])
    check(waitForFile(log, containing: "refusing pid", timeout: 10), "helper: refuses a non-numeric pid")
} catch {
    check(false, "helper: setup (\(error))")
}

// 21. update verification against a real ad-hoc signed bundle (no network)
func makeSignedApp(in parent: URL, id: String, version: String) -> URL {
    let app = parent.appendingPathComponent("Interseptor.app")
    let macos = app.appendingPathComponent("Contents/MacOS")
    try! FileManager.default.createDirectory(at: macos, withIntermediateDirectories: true)
    let plist: [String: Any] = ["CFBundleIdentifier": id, "CFBundleShortVersionString": version,
                                "CFBundleExecutable": "InterseptorApp", "CFBundlePackageType": "APPL"]
    (plist as NSDictionary).write(to: app.appendingPathComponent("Contents/Info.plist"), atomically: true)
    try! FileManager.default.copyItem(atPath: "/usr/bin/true", toPath: macos.appendingPathComponent("InterseptorApp").path)
    let r = UpdateVerifier.run("/usr/bin/codesign", ["--force", "--sign", "-", app.path])
    check(r.status == 0, "verify: fixture signed (\(r.output))")
    return app
}
func zipApp(_ app: URL, to zip: URL) {
    let r = UpdateVerifier.run("/usr/bin/ditto", ["-c", "-k", "--keepParent", app.path, zip.path])
    check(r.status == 0, "verify: fixture zipped (\(r.output))")
}
func verifyFailure(_ body: () throws -> Void) -> UpdateVerifier.Failure? {
    do { try body(); return nil } catch { return error as? UpdateVerifier.Failure }
}
do {
    let root = dir.appendingPathComponent("verify")
    let app = makeSignedApp(in: root.appendingPathComponent("src"), id: "com.example.interseptor", version: "2.18.1")
    let zip = root.appendingPathComponent("Interseptor-v2.18.1-unsigned.zip")
    zipApp(app, to: zip)
    let shasum = UpdateVerifier.run("/usr/bin/shasum", ["-a", "256", zip.path]).output.prefix(64)
    let digest = try UpdateVerifier.sha256Hex(of: zip)
    check(digest == String(shasum), "verify: streamed SHA-256 matches shasum")
    let sums = "\(String(repeating: "0", count: 64))  x\(zip.lastPathComponent)\n\(digest)  \(zip.lastPathComponent)\n"
    let out = root.appendingPathComponent("out")
    let verified = try UpdateVerifier.verify(zip: zip, checksums: sums, extractTo: out,
                                             bundleID: "com.example.interseptor", version: "v2.18.1")
    check(verified.lastPathComponent == "Interseptor.app", "verify: good update accepted")
    let xattr = UpdateVerifier.run("/usr/bin/xattr", ["-p", "com.apple.quarantine", verified.path])
    check(xattr.status != 0, "verify: extracted app carries no quarantine")

    let wrongSums = "\(String(repeating: "f", count: 64))  \(zip.lastPathComponent)"
    check(verifyFailure { _ = try UpdateVerifier.verify(zip: zip, checksums: wrongSums, extractTo: out,
                                                        bundleID: "com.example.interseptor", version: "2.18.1") }
          == .checksumMismatch, "verify: checksum mismatch refused")
    check(verifyFailure { _ = try UpdateVerifier.verify(zip: zip, checksums: "\(digest)  other.zip", extractTo: out,
                                                        bundleID: "com.example.interseptor", version: "2.18.1") }
          == .checksumMissing(zip.lastPathComponent), "verify: missing checksum line refused")
    check(verifyFailure { _ = try UpdateVerifier.verify(zip: zip, checksums: sums, extractTo: out,
                                                        bundleID: "com.example.other", version: "2.18.1") }
          == .wrongIdentifier("com.example.interseptor"), "verify: other bundle id refused")
    check(verifyFailure { _ = try UpdateVerifier.verify(zip: zip, checksums: sums, extractTo: out,
                                                        bundleID: "com.example.interseptor", version: "2.18.2") }
          == .wrongVersion("2.18.1"), "verify: version mismatch refused")
    // Swapping the signed executable breaks the seal.
    let exe = out.appendingPathComponent("Interseptor.app/Contents/MacOS/InterseptorApp")
    try FileManager.default.removeItem(at: exe)
    try FileManager.default.copyItem(atPath: "/bin/echo", toPath: exe.path)
    if case .signatureInvalid? = verifyFailure({
        try UpdateVerifier.validateApp(at: out.appendingPathComponent("Interseptor.app"), bundleID: "com.example.interseptor", version: "2.18.1")
    }) {
        check(true, "verify: tampered bundle refused")
    } else {
        check(false, "verify: tampered bundle refused")
    }
    // A ZIP without Interseptor.app, and one that is not a ZIP.
    let other = root.appendingPathComponent("Other.app")
    try FileManager.default.createDirectory(at: other.appendingPathComponent("Contents"), withIntermediateDirectories: true)
    let otherZip = root.appendingPathComponent("other.zip")
    zipApp(other, to: otherZip)
    check(verifyFailure { _ = try UpdateVerifier.extract(otherZip, into: out) } == .appMissing, "verify: archive without the app refused")
    let junk = root.appendingPathComponent("junk.zip")
    try "not a zip".write(to: junk, atomically: true, encoding: .utf8)
    if case .extractFailed? = verifyFailure({ _ = try UpdateVerifier.extract(junk, into: out) }) {
        check(true, "verify: corrupt archive refused")
    } else {
        check(false, "verify: corrupt archive refused")
    }
} catch {
    check(false, "verify: setup (\(error))")
}

// 22. optional: verify a real built bundle, e.g.
// UPDATE_E2E_APP=/tmp/x/Interseptor.app packaging/macos/shell-tests/run.sh
if let real = env["UPDATE_E2E_APP"] {
    do {
        let appURL = URL(fileURLWithPath: real)
        let info = NSDictionary(contentsOf: appURL.appendingPathComponent("Contents/Info.plist")) as? [String: Any] ?? [:]
        let id = info["CFBundleIdentifier"] as? String ?? "", v = info["CFBundleShortVersionString"] as? String ?? ""
        let root = dir.appendingPathComponent("e2e")
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        let zip = root.appendingPathComponent("Interseptor-v\(v)-unsigned.zip")
        zipApp(appURL, to: zip)
        let sums = "\(try UpdateVerifier.sha256Hex(of: zip))  \(zip.lastPathComponent)\n"
        _ = try UpdateVerifier.verify(zip: zip, checksums: sums, extractTo: root.appendingPathComponent("x"), bundleID: id, version: v)
        check(true, "e2e: real bundle \(id) \(v) verified")
    } catch {
        check(false, "e2e: real bundle verification (\(error))")
    }
}

// exit() skips the top-level defer, so remove the fixtures explicitly.
try? FileManager.default.removeItem(at: dir)
print(failures == 0 ? "ALL PASSED" : "\(failures) FAILED")
exit(failures == 0 ? 0 : 1)
