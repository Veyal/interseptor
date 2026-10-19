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

print(failures == 0 ? "ALL PASSED" : "\(failures) FAILED")
exit(failures == 0 ? 0 : 1)
