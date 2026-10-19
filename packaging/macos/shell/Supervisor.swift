import Foundation

/// Runs `interseptor-macapp --shell` and translates its stdout protocol
/// (INTERSEPTOR_READY <url> / INTERSEPTOR_ERROR <message>) into events.
///
/// The supervisor's stdin is a pipe this process never writes to: if the shell
/// dies, the pipe closes and the supervisor stops the server it started.
final class Supervisor {
    enum Event {
        case ready(URL)
        case error(String)
        /// The supervisor exited without being asked to.
        case exited(Int32)
    }

    var onEvent: ((Event) -> Void)?

    /// One launched process and everything tied to it. State is keyed by this
    /// object's identity, so a late exit or stdout line from a process that has
    /// already been replaced (or abandoned after a stuck stop) can never touch
    /// the current one.
    private final class Run {
        let process: Process
        let stdin: Pipe
        var lineBuffer = Data() // touched only on the stdout reader queue
        var stopping = false
        var exited = false
        var completions: [() -> Void] = []
        init(process: Process, stdin: Pipe) {
            self.process = process
            self.stdin = stdin
        }
    }

    private var current: Run?

    /// After SIGKILL, how long stop() waits for the exit before giving up on a
    /// process the kernel will not reap (so a restart can never wedge).
    static let killGrace: TimeInterval = 2

    var isRunning: Bool { current?.process.isRunning ?? false }

    static var logURL: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Logs/Interseptor.log")
    }

    func start(executable: URL, environment: [String: String], arguments: [String] = ["--shell"]) throws {
        let p = Process()
        p.executableURL = executable
        p.arguments = arguments
        p.environment = environment

        let input = Pipe()
        let output = Pipe()
        p.standardInput = input
        p.standardOutput = output
        p.standardError = Self.logHandle() ?? FileHandle.nullDevice

        let run = Run(process: p, stdin: input)
        output.fileHandleForReading.readabilityHandler = { [weak self, weak run] handle in
            let data = handle.availableData
            if data.isEmpty {
                handle.readabilityHandler = nil
                return
            }
            guard let self, let run else { return }
            self.consume(data, from: run)
        }
        p.terminationHandler = { [weak self] proc in
            let status = proc.terminationStatus
            DispatchQueue.main.async { self?.handleExit(run, status) }
        }

        try p.run()
        current = run
    }

    /// Asks the supervisor to stop (SIGTERM, which it forwards to a server it
    /// owns). completion is called from the exit handler once that process has
    /// really exited, so a restart never races a live predecessor. If it has
    /// not exited after timeout it is SIGKILLed; if even that does not produce
    /// an exit within killGrace the process is abandoned and completion runs
    /// anyway, so the wait is always bounded.
    func stop(timeout: TimeInterval, completion: @escaping () -> Void) {
        guard let run = current, run.process.isRunning else {
            completion()
            return
        }
        run.completions.append(completion)
        guard !run.stopping else { return }
        run.stopping = true
        run.process.terminate()
        DispatchQueue.main.asyncAfter(deadline: .now() + timeout) { [weak self, weak run] in
            guard let self, let run, !run.exited else { return }
            kill(run.process.processIdentifier, SIGKILL)
            DispatchQueue.main.asyncAfter(deadline: .now() + Self.killGrace) { [weak self, weak run] in
                guard let self, let run, !run.exited else { return }
                self.abandon(run)
            }
        }
    }

    private func abandon(_ run: Run) {
        if current === run { current = nil }
        finish(run)
    }

    private func finish(_ run: Run) {
        run.exited = true
        let done = run.completions
        run.completions = []
        done.forEach { $0() }
    }

    private func handleExit(_ run: Run, _ status: Int32) {
        guard !run.exited else { return } // abandoned earlier; its completions already ran
        let isCurrent = current === run
        if isCurrent { current = nil }
        if run.stopping {
            finish(run)
        } else {
            run.exited = true
            // An exit from a process that was already replaced is stale.
            if isCurrent { onEvent?(.exited(status)) }
        }
    }

    private func consume(_ data: Data, from run: Run) {
        run.lineBuffer.append(data)
        while let nl = run.lineBuffer.firstIndex(of: 0x0a) {
            let lineData = run.lineBuffer[run.lineBuffer.startIndex..<nl]
            run.lineBuffer.removeSubrange(run.lineBuffer.startIndex...nl)
            guard let line = String(data: lineData, encoding: .utf8) else { continue }
            if let event = Self.parse(line) {
                DispatchQueue.main.async { [weak self] in
                    guard let self, self.current === run, !run.stopping else { return }
                    self.onEvent?(event)
                }
            }
        }
        if run.lineBuffer.count > 64 << 10 { run.lineBuffer.removeAll() }
    }

    static func parse(_ line: String) -> Event? {
        let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.hasPrefix("INTERSEPTOR_READY ") {
            let raw = String(trimmed.dropFirst("INTERSEPTOR_READY ".count))
            return URL(string: raw).map(Event.ready)
        }
        if trimmed.hasPrefix("INTERSEPTOR_ERROR ") {
            return .error(String(trimmed.dropFirst("INTERSEPTOR_ERROR ".count)))
        }
        return nil
    }

    private static func logHandle() -> FileHandle? {
        let url = logURL
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        if !FileManager.default.fileExists(atPath: url.path) {
            FileManager.default.createFile(atPath: url.path, contents: nil)
        }
        guard let h = try? FileHandle(forWritingTo: url) else { return nil }
        h.seekToEndOfFile()
        return h
    }
}
