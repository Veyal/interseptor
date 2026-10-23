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
        /// The log file handed to the process as stderr; nil when it fell back
        /// to the shared null device, which must never be closed.
        let log: FileHandle?
        var lineBuffer = Data() // touched only on the stdout reader queue
        var stopping = false
        var exited = false
        /// Its unexpected exit was reported. The supervisor prints
        /// INTERSEPTOR_ERROR and then exits, and the exit can reach the main
        /// queue before that stdout line does: the line is still delivered.
        var exitReported = false
        var completions: [() -> Void] = []
        init(process: Process, stdin: Pipe, log: FileHandle?) {
            self.process = process
            self.stdin = stdin
            self.log = log
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
        let log = Self.logHandle()
        p.standardInput = input
        p.standardOutput = output
        p.standardError = log ?? FileHandle.nullDevice

        let run = Run(process: p, stdin: input, log: log)
        // The handler holds the run until EOF, so a line that arrives after the
        // exit was handled (and the run released) is still parsed. Clearing the
        // handler at EOF breaks that cycle.
        output.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            if data.isEmpty {
                handle.readabilityHandler = nil
                try? handle.close()
                return
            }
            self?.consume(data, from: run)
        }
        p.terminationHandler = { [weak self] proc in
            let status = proc.terminationStatus
            DispatchQueue.main.async { self?.handleExit(run, status) }
        }

        do {
            try p.run()
        } catch {
            output.fileHandleForReading.readabilityHandler = nil
            try? output.fileHandleForReading.close()
            release(run)
            throw error
        }
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
        release(run)
        finish(run)
    }

    private func finish(_ run: Run) {
        run.exited = true
        let done = run.completions
        run.completions = []
        done.forEach { $0() }
    }

    /// Drops the run's own file descriptors and its exit handler (which holds
    /// the run). Stdout is closed by its reader at EOF, after the last line.
    private func release(_ run: Run) {
        run.process.terminationHandler = nil
        try? run.stdin.fileHandleForWriting.close()
        try? run.log?.close()
    }

    private func handleExit(_ run: Run, _ status: Int32) {
        guard !run.exited else { return } // abandoned earlier; its completions already ran
        let isCurrent = current === run
        if isCurrent { current = nil }
        release(run)
        if run.stopping {
            finish(run)
        } else {
            run.exited = true
            // An exit from a process that was already replaced is stale.
            if isCurrent {
                run.exitReported = true
                onEvent?(.exited(status))
            }
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
                    guard let self, self.shouldDeliver(event, from: run) else { return }
                    self.onEvent?(event)
                }
            }
        }
        if run.lineBuffer.count > 64 << 10 { run.lineBuffer.removeAll() }
    }

    /// Lines from the current run are delivered unless it is being stopped. An
    /// error line that lost the race with its own run's reported exit is still
    /// delivered — it explains that exit — as long as no newer run has started.
    private func shouldDeliver(_ event: Event, from run: Run) -> Bool {
        if current === run { return !run.stopping }
        if case .error = event { return run.exitReported && current == nil }
        return false
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
        return appendHandle(at: url)
    }

    /// Opens path for appending with O_APPEND, as the Go supervisor opens the
    /// same log: every write lands at the current end, so neither side can
    /// overwrite the other's lines (a seek-to-end-then-write fd would).
    static func appendHandle(at url: URL) -> FileHandle? {
        let fd = open(url.path, O_WRONLY | O_APPEND | O_CREAT | O_CLOEXEC, 0o644)
        guard fd >= 0 else { return nil }
        return FileHandle(fileDescriptor: fd, closeOnDealloc: true)
    }
}
