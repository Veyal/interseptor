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

print(failures == 0 ? "ALL PASSED" : "\(failures) FAILED")
exit(failures == 0 ? 0 : 1)
