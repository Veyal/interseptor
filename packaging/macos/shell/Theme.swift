import AppKit

/// Window chrome colours, kept in step with the web UI's --bg token.
struct Theme {
    /// --bg of the default dark theme in internal/control/ui/app.css.
    static let defaultBackgroundHex = "#111318"

    var background: NSColor
    var dark: Bool

    static var initial: Theme {
        let bg = Settings.lastBackground.flatMap(NSColor.init(hex:)) ?? NSColor(hex: defaultBackgroundHex)!
        return Theme(background: bg, dark: Settings.lastScheme != "light")
    }

    var appearance: NSAppearance? { NSAppearance(named: dark ? .darkAqua : .aqua) }
    var foreground: NSColor { dark ? NSColor(white: 0.92, alpha: 1) : NSColor(white: 0.1, alpha: 1) }
    var secondary: NSColor { dark ? NSColor(white: 0.62, alpha: 1) : NSColor(white: 0.38, alpha: 1) }
}

extension NSColor {
    /// Parses #rgb or #rrggbb.
    convenience init?(hex: String) {
        var s = hex.trimmingCharacters(in: .whitespaces)
        if s.hasPrefix("#") { s.removeFirst() }
        if s.count == 3 { s = s.map { "\($0)\($0)" }.joined() }
        guard s.count == 6, let v = UInt32(s, radix: 16) else { return nil }
        self.init(srgbRed: CGFloat((v >> 16) & 0xff) / 255,
                  green: CGFloat((v >> 8) & 0xff) / 255,
                  blue: CGFloat(v & 0xff) / 255, alpha: 1)
    }
}
