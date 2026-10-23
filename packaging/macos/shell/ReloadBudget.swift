import Foundation

/// Bounds automatic reloads after the web content process dies: at most limit
/// within window. A page that crashes its content process on every load would
/// otherwise be reloaded forever.
struct ReloadBudget {
    let limit: Int
    let window: TimeInterval
    private var times: [Date] = []

    init(limit: Int = 3, window: TimeInterval = 30) {
        self.limit = limit
        self.window = window
    }

    /// Records a reload at now and reports whether it is within budget.
    mutating func allow(at now: Date = Date()) -> Bool {
        times.removeAll { now.timeIntervalSince($0) >= window }
        guard times.count < limit else { return false }
        times.append(now)
        return true
    }

    /// Forgets past reloads, e.g. when the user explicitly retries.
    mutating func reset() { times.removeAll() }
}
