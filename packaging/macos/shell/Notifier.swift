import AppKit
import UserNotifications

/// Posts "Request held in Intercept" when the held count rises while the app
/// is in the background. Authorization is requested lazily, on first need.
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
    var onOpenIntercept: (() -> Void)?
    private var authorized: Bool?
    private var lastHeld = 0

    override init() {
        super.init()
        UNUserNotificationCenter.current().delegate = self
    }

    func heldChanged(_ status: DesktopStatus, appActive: Bool) {
        defer { lastHeld = status.held }
        guard status.held > lastHeld, !appActive else { return }
        let body = status.heldLatest?.summary ?? "\(status.held) request\(status.held == 1 ? "" : "s") waiting"
        post(title: "Request held in Intercept", body: body)
    }

    private func post(title: String, body: String) {
        let center = UNUserNotificationCenter.current()
        let send = {
            let content = UNMutableNotificationContent()
            content.title = title
            content.body = body
            content.sound = .default
            center.add(UNNotificationRequest(identifier: "held-\(UUID().uuidString)", content: content, trigger: nil))
        }
        switch authorized {
        case .some(true): send()
        case .some(false): return
        case .none:
            center.requestAuthorization(options: [.alert, .sound, .badge]) { [weak self] granted, _ in
                DispatchQueue.main.async {
                    self?.authorized = granted
                    if granted { send() }
                }
            }
        }
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler completionHandler: @escaping () -> Void) {
        DispatchQueue.main.async { [weak self] in
            self?.onOpenIntercept?()
            completionHandler()
        }
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler([.banner, .sound])
    }
}
