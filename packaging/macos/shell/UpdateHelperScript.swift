import Foundation

/// The detached helper that swaps the app bundle once the app has quit. A
/// running app cannot safely replace its own bundle, so the shell writes this
/// script to its cache, spawns it in a new session and exits; the script waits
/// for the old process, swaps the bundles, restores the old one on failure, and
/// relaunches.
///
/// The script text is constant: every path reaches it as a positional argument
/// (argv, never interpolated into shell source), so spaces, quotes and $ in a
/// path cannot change what runs.
enum UpdateHelperScript {
    /// How long the helper waits for the app to exit (300 × 0.2s).
    static let waitSeconds = 60

    static let source = """
    #!/bin/sh
    # Interseptor update helper, written by the app before it quits.
    # Arguments: <pid> <app path> <new app path> <log path> <relaunch 0|1>
    pid=$1 app=$2 new=$3 log=$4 relaunch=$5
    exec </dev/null >>"$log" 2>&1
    say() { echo "$(/bin/date '+%Y-%m-%d %H:%M:%S') update: $*"; }
    reopen() { if [ "$relaunch" = 1 ]; then /usr/bin/open "$1" || say "could not relaunch $1"; fi; }
    case "$pid" in ''|*[!0-9]*) say "refusing pid '$pid'"; exit 2 ;; esac
    case "$app" in /*.app) ;; *) say "refusing app path '$app'"; exit 2 ;; esac
    case "$new" in /*.app) ;; *) say "refusing update path '$new'"; exit 2 ;; esac
    [ -d "$new" ] || { say "update missing: $new"; exit 2; }
    say "waiting for pid $pid to exit"
    n=0
    while kill -0 "$pid" 2>/dev/null; do
      n=$((n + 1))
      if [ "$n" -ge \(waitSeconds * 5) ]; then say "pid $pid still running; not installing"; exit 1; fi
      sleep 0.2
    done
    backup="$app.old-$$"
    i=0
    while [ -e "$backup" ]; do i=$((i + 1)); backup="$app.old-$$-$i"; done
    if ! /bin/mv "$app" "$backup"; then say "could not move $app aside; not installing"; reopen "$app"; exit 1; fi
    ok=0
    if [ ! -e "$app" ]; then
      # Same volume: a rename. Across volumes mv copies and may leave a partial
      # copy behind on failure, so clear it and try ditto.
      if /bin/mv "$new" "$app"; then ok=1
      else
        /bin/rm -rf "$app"
        if /usr/bin/ditto "$new" "$app"; then ok=1; /bin/rm -rf "$new"; else /bin/rm -rf "$app"; fi
      fi
    fi
    if [ "$ok" = 1 ]; then
      /bin/rm -rf "$backup" 2>/dev/null || say "installed, but could not remove the previous version at $backup; delete it manually"
      say "installed $app"
    else
      say "install failed; restoring the previous version"
      if [ -e "$app" ] || ! /bin/mv "$backup" "$app"; then say "RESTORE FAILED: the previous version is at $backup"; exit 1; fi
    fi
    reopen "$app"
    """

    static func arguments(pid: Int32, app: URL, newApp: URL, log: URL, relaunch: Bool) -> [String] {
        [String(pid), app.path, newApp.path, log.path, relaunch ? "1" : "0"]
    }

    static var logURL: URL {
        FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Logs/Interseptor-update.log")
    }

    /// Writes the script owner-only executable.
    static func write(to url: URL) throws {
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try source.write(to: url, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: url.path)
    }

    /// Starts `/bin/sh script args…` in its own session (POSIX_SPAWN_SETSID),
    /// so it is not in the app's process group and survives the app's exit,
    /// with only stdin/out/err (all /dev/null) inherited. Not waited on.
    @discardableResult
    static func spawn(script: URL, arguments: [String]) throws -> pid_t {
        var attr: posix_spawnattr_t?
        posix_spawnattr_init(&attr)
        defer { posix_spawnattr_destroy(&attr) }
        posix_spawnattr_setflags(&attr, Int16(POSIX_SPAWN_SETSID | POSIX_SPAWN_CLOEXEC_DEFAULT))
        var actions: posix_spawn_file_actions_t?
        posix_spawn_file_actions_init(&actions)
        defer { posix_spawn_file_actions_destroy(&actions) }
        posix_spawn_file_actions_addopen(&actions, 0, "/dev/null", O_RDONLY, 0)
        posix_spawn_file_actions_addopen(&actions, 1, "/dev/null", O_WRONLY, 0)
        posix_spawn_file_actions_addopen(&actions, 2, "/dev/null", O_WRONLY, 0)

        let argv = ["/bin/sh", script.path] + arguments
        var cargs: [UnsafeMutablePointer<CChar>?] = argv.map { strdup($0) } + [nil]
        defer { cargs.forEach { free($0) } }
        var pid: pid_t = 0
        let rc = posix_spawn(&pid, "/bin/sh", &actions, &attr, &cargs, environ)
        guard rc == 0 else { throw NSError(domain: NSPOSIXErrorDomain, code: Int(rc)) }
        return pid
    }
}
