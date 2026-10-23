import AppKit

/// The Software Update window: app icon on the left, text, optional release
/// notes and progress on the right, buttons along the bottom. It has no update
/// logic of its own; Updater describes each state as a Content and the window
/// redraws itself from it, so the same window moves from "available" through
/// download and verification to "ready" without flicker.
@MainActor
final class UpdateWindowController: NSWindowController, NSWindowDelegate, NSTextViewDelegate {
    struct Button {
        enum Role { case normal, defaultAction, cancel }
        var title: String
        var role: Role = .normal
        /// Sits on the left, apart from the rest (Skip This Version).
        var leading = false
        var action: () -> Void
    }

    enum ProgressStyle { case hidden, indeterminate, fraction(Double) }

    struct Content {
        var headline: String
        var subtitle: String = ""
        var notes: String? = nil
        var warning: String? = nil
        var progress: ProgressStyle = .hidden
        var detail: String? = nil
        var buttons: [Button] = []
    }

    /// Called when the user closes the window with its close button or ⌘W.
    var onClose: (() -> Void)?

    private static let textWidth: CGFloat = 460

    private let headline = NSTextField(wrappingLabelWithString: "")
    private let subtitle = NSTextField(wrappingLabelWithString: "")
    private let notesTitle = NSTextField(labelWithString: "Release Notes:")
    private let notesScroll = NSTextView.scrollableTextView()
    private let warning = NSTextField(wrappingLabelWithString: "")
    private let progress = NSProgressIndicator()
    private let detail = NSTextField(labelWithString: "")
    private let buttonRow = NSStackView()
    private var handlers: [() -> Void] = []
    private var closingProgrammatically = false

    private var notesView: NSTextView { notesScroll.documentView as! NSTextView }

    init() {
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 600, height: 200),
                              styleMask: [.titled, .closable], backing: .buffered, defer: false)
        window.title = "Software Update"
        window.isReleasedWhenClosed = false
        window.tabbingMode = .disallowed
        super.init(window: window)
        window.delegate = self
        build(in: window)
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    private func build(in window: NSWindow) {
        let icon = NSImageView()
        icon.image = NSApp.applicationIconImage
        icon.imageScaling = .scaleProportionallyUpOrDown
        icon.translatesAutoresizingMaskIntoConstraints = false
        icon.widthAnchor.constraint(equalToConstant: 64).isActive = true
        icon.heightAnchor.constraint(equalToConstant: 64).isActive = true

        headline.font = .boldSystemFont(ofSize: 14)
        subtitle.font = .systemFont(ofSize: 12)
        warning.font = .systemFont(ofSize: 12)
        warning.textColor = .systemOrange
        notesTitle.font = .boldSystemFont(ofSize: 12)
        detail.font = .monospacedDigitSystemFont(ofSize: 11, weight: .regular)
        detail.textColor = .secondaryLabelColor
        for label in [headline, subtitle, warning] {
            label.preferredMaxLayoutWidth = Self.textWidth
            label.isSelectable = true
        }

        notesView.isEditable = false
        notesView.isSelectable = true
        notesView.textContainerInset = NSSize(width: 6, height: 6)
        notesView.delegate = self
        notesScroll.borderType = .bezelBorder
        notesScroll.hasVerticalScroller = true
        notesScroll.translatesAutoresizingMaskIntoConstraints = false
        notesScroll.heightAnchor.constraint(equalToConstant: 220).isActive = true

        progress.style = .bar
        progress.minValue = 0
        progress.maxValue = 1

        let text = NSStackView(views: [headline, subtitle, notesTitle, notesScroll, warning, progress, detail])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 8
        text.detachesHiddenViews = true
        text.setCustomSpacing(12, after: subtitle)
        text.translatesAutoresizingMaskIntoConstraints = false
        for v in [headline, subtitle, notesScroll, warning, progress] {
            v.widthAnchor.constraint(equalToConstant: Self.textWidth).isActive = true
        }

        let top = NSStackView(views: [icon, text])
        top.orientation = .horizontal
        top.alignment = .top
        top.spacing = 16

        buttonRow.orientation = .horizontal
        buttonRow.spacing = 12

        let root = NSStackView(views: [top, buttonRow])
        root.orientation = .vertical
        root.alignment = .trailing
        root.spacing = 20
        root.edgeInsets = NSEdgeInsets(top: 20, left: 20, bottom: 20, right: 20)
        root.translatesAutoresizingMaskIntoConstraints = false
        buttonRow.leadingAnchor.constraint(equalTo: root.leadingAnchor, constant: 20).isActive = true

        let content = NSView()
        content.addSubview(root)
        NSLayoutConstraint.activate([
            root.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            root.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            root.topAnchor.constraint(equalTo: content.topAnchor),
            root.bottomAnchor.constraint(equalTo: content.bottomAnchor),
        ])
        window.contentView = content
    }

    /// Redraws the window for c and brings it forward.
    func show(_ c: Content) {
        headline.stringValue = c.headline
        subtitle.stringValue = c.subtitle
        subtitle.isHidden = c.subtitle.isEmpty
        notesTitle.isHidden = c.notes == nil
        notesScroll.isHidden = c.notes == nil
        if let notes = c.notes {
            notesView.textStorage?.setAttributedString(Self.render(notes))
            notesView.scrollToBeginningOfDocument(nil)
        }
        warning.stringValue = c.warning ?? ""
        warning.isHidden = c.warning == nil
        switch c.progress {
        case .hidden:
            progress.stopAnimation(nil)
            progress.isHidden = true
        case .indeterminate:
            progress.isHidden = false
            progress.isIndeterminate = true
            progress.startAnimation(nil)
        case .fraction(let f):
            progress.isHidden = false
            progress.stopAnimation(nil)
            progress.isIndeterminate = false
            progress.doubleValue = min(max(f, 0), 1)
        }
        detail.stringValue = c.detail ?? ""
        detail.isHidden = c.detail == nil
        setButtons(c.buttons)

        guard let window else { return }
        window.contentView?.layoutSubtreeIfNeeded()
        let size = window.contentView?.fittingSize ?? window.frame.size
        if window.isVisible {
            // Keep the top edge where it is while the content grows or shrinks.
            var frame = window.frameRect(forContentRect: NSRect(origin: .zero, size: size))
            frame.origin = NSPoint(x: window.frame.minX, y: window.frame.maxY - frame.height)
            window.setFrame(frame, display: true)
        } else {
            window.setContentSize(size)
            window.center()
        }
        NSApp.activate(ignoringOtherApps: true)
        showWindow(nil)
        window.makeKeyAndOrderFront(nil)
    }

    /// Updates only the progress bar and detail line (download ticks).
    func setProgress(_ fraction: Double, detail text: String) {
        progress.isIndeterminate = false
        progress.doubleValue = min(max(fraction, 0), 1)
        detail.stringValue = text
    }

    var isShown: Bool { window?.isVisible ?? false }

    func dismiss() {
        closingProgrammatically = true
        window?.close()
        closingProgrammatically = false
    }

    func windowWillClose(_ notification: Notification) {
        if !closingProgrammatically { onClose?() }
    }

    private func setButtons(_ buttons: [Button]) {
        buttonRow.arrangedSubviews.forEach { $0.removeFromSuperview() }
        handlers = buttons.map(\.action)
        var leading: [NSView] = [], trailing: [NSView] = []
        for (i, spec) in buttons.enumerated() {
            let b = NSButton(title: spec.title, target: self, action: #selector(pressed(_:)))
            b.tag = i
            b.bezelStyle = .rounded
            switch spec.role {
            case .defaultAction: b.keyEquivalent = "\r"
            case .cancel: b.keyEquivalent = "\u{1b}"
            case .normal: break
            }
            if spec.leading { leading.append(b) } else { trailing.append(b) }
        }
        leading.forEach { buttonRow.addArrangedSubview($0) }
        let spacer = NSView()
        spacer.setContentHuggingPriority(.init(1), for: .horizontal)
        buttonRow.addArrangedSubview(spacer)
        trailing.forEach { buttonRow.addArrangedSubview($0) }
    }

    @objc private func pressed(_ sender: NSButton) {
        guard handlers.indices.contains(sender.tag) else { return }
        handlers[sender.tag]()
    }

    // Links in release notes go to the browser, through the same scheme
    // allowlist as links in the web view.
    func textView(_ textView: NSTextView, clickedOnLink link: Any, at charIndex: Int) -> Bool {
        let url = (link as? URL) ?? (link as? String).flatMap(URL.init(string:))
        if ExternalLinks.canOpenExternally(url), let url { NSWorkspace.shared.open(url) }
        return true
    }

    /// Release notes Markdown as styled text. AppKit does not draw Foundation's
    /// inline presentation intents itself, so bold, italic and code are mapped
    /// to fonts here; block syntax was flattened by ReleaseNotes first.
    static func render(_ markdown: String) -> NSAttributedString {
        let base = NSFont.systemFont(ofSize: 12)
        let source = ReleaseNotes.inlineMarkdown(markdown)
        let attrs: [NSAttributedString.Key: Any] = [.font: base, .foregroundColor: NSColor.textColor]
        if source.isEmpty { return NSAttributedString(string: "No release notes.", attributes: attrs) }
        let options = AttributedString.MarkdownParsingOptions(
            allowsExtendedAttributes: false, interpretedSyntax: .inlineOnlyPreservingWhitespace,
            failurePolicy: .returnPartiallyParsedIfPossible)
        guard let parsed = try? NSAttributedString(markdown: source, options: options, baseURL: nil) else {
            return NSAttributedString(string: source, attributes: attrs)
        }
        let out = NSMutableAttributedString(attributedString: parsed)
        let all = NSRange(location: 0, length: out.length)
        out.addAttributes(attrs, range: all)
        out.enumerateAttribute(.inlinePresentationIntent, in: all) { value, range, _ in
            guard let raw = (value as? NSNumber)?.uintValue else { return }
            let intent = InlinePresentationIntent(rawValue: raw)
            var font = base
            if intent.contains(.code) { font = .monospacedSystemFont(ofSize: 11, weight: .regular) }
            if intent.contains(.stronglyEmphasized) { font = NSFontManager.shared.convert(font, toHaveTrait: .boldFontMask) }
            if intent.contains(.emphasized) { font = NSFontManager.shared.convert(font, toHaveTrait: .italicFontMask) }
            out.addAttribute(.font, value: font, range: range)
        }
        return out
    }
}
