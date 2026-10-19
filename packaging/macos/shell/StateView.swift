import AppKit

/// The native overlay shown while the proxy starts or after it fails: drawn in
/// the window's own colours so there is never a white flash before the UI.
final class StateView: NSView {
    struct Action {
        var title: String
        var handler: () -> Void
    }

    private let icon = NSImageView()
    private let title = NSTextField(labelWithString: "Interseptor")
    private let message = NSTextField(wrappingLabelWithString: "")
    private let spinner = NSProgressIndicator()
    private let buttons = NSStackView()
    private var handlers: [() -> Void] = []

    /// The overlay fills the window, title bar included (the title bar is
    /// unified with the page), so a press anywhere on it moves the window.
    override var mouseDownCanMoveWindow: Bool { true }

    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true

        icon.image = NSApp.applicationIconImage
        icon.imageScaling = .scaleProportionallyUpOrDown
        icon.translatesAutoresizingMaskIntoConstraints = false
        icon.widthAnchor.constraint(equalToConstant: 72).isActive = true
        icon.heightAnchor.constraint(equalToConstant: 72).isActive = true

        title.font = .systemFont(ofSize: 22, weight: .semibold)
        message.font = .systemFont(ofSize: 13)
        message.alignment = .center
        message.preferredMaxLayoutWidth = 460
        spinner.style = .spinning
        spinner.controlSize = .small
        buttons.orientation = .horizontal
        buttons.spacing = 12

        let stack = NSStackView(views: [icon, title, spinner, message, buttons])
        stack.orientation = .vertical
        stack.alignment = .centerX
        stack.spacing = 14
        stack.setCustomSpacing(18, after: icon)
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.centerXAnchor.constraint(equalTo: centerXAnchor),
            stack.centerYAnchor.constraint(equalTo: centerYAnchor, constant: -20),
            stack.widthAnchor.constraint(lessThanOrEqualTo: widthAnchor, constant: -48),
        ])
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    func apply(_ theme: Theme) {
        layer?.backgroundColor = theme.background.cgColor
        appearance = theme.appearance
        title.textColor = theme.foreground
        message.textColor = theme.secondary
    }

    func showLoading(_ text: String) {
        spinner.isHidden = false
        spinner.startAnimation(nil)
        message.stringValue = text
        setActions([])
    }

    func showError(title heading: String, message text: String, actions: [Action]) {
        spinner.stopAnimation(nil)
        spinner.isHidden = true
        title.stringValue = heading
        message.stringValue = text
        setActions(actions)
    }

    func resetTitle() { title.stringValue = "Interseptor" }

    private func setActions(_ actions: [Action]) {
        buttons.arrangedSubviews.forEach { $0.removeFromSuperview() }
        handlers = actions.map(\.handler)
        for (i, a) in actions.enumerated() {
            let b = NSButton(title: a.title, target: self, action: #selector(tapped(_:)))
            b.bezelStyle = .rounded
            b.tag = i
            if i == actions.count - 1 { b.keyEquivalent = "\r" }
            buttons.addArrangedSubview(b)
        }
        buttons.isHidden = actions.isEmpty
    }

    @objc private func tapped(_ sender: NSButton) {
        if handlers.indices.contains(sender.tag) { handlers[sender.tag]() }
    }
}
