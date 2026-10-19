import AppKit

/// The Interseptor mark (an octagon crossed by two opposing arrows) drawn as a
/// template image for the menu bar, so it follows the system's light/dark tint.
enum StatusGlyph {
    static func image(size: CGFloat = 18) -> NSImage {
        let img = NSImage(size: NSSize(width: size, height: size), flipped: true) { rect in
            guard let ctx = NSGraphicsContext.current?.cgContext else { return false }
            // The source artwork uses a 24-unit viewBox; inset slightly so the
            // octagon's corners are not clipped at menu-bar size.
            let scale = rect.width / 26
            ctx.translateBy(x: scale, y: scale)
            ctx.scaleBy(x: scale, y: scale)

            let octagon = CGMutablePath()
            octagon.addLines(between: [
                CGPoint(x: 6, y: 0), CGPoint(x: 18, y: 0), CGPoint(x: 24, y: 6), CGPoint(x: 24, y: 18),
                CGPoint(x: 18, y: 24), CGPoint(x: 6, y: 24), CGPoint(x: 0, y: 18), CGPoint(x: 0, y: 6),
            ])
            octagon.closeSubpath()
            ctx.setFillColor(NSColor.black.cgColor)
            ctx.addPath(octagon)
            ctx.fillPath()

            // Arrows are knocked out of the fill.
            ctx.setBlendMode(.clear)
            ctx.setLineWidth(2.4)
            ctx.setLineCap(.square)
            ctx.setLineJoin(.miter)
            let arrows = CGMutablePath()
            arrows.move(to: CGPoint(x: 5, y: 8.5)); arrows.addLine(to: CGPoint(x: 17.5, y: 8.5))
            arrows.addLine(to: CGPoint(x: 14.5, y: 5.5))
            arrows.move(to: CGPoint(x: 19, y: 15.5)); arrows.addLine(to: CGPoint(x: 6.5, y: 15.5))
            arrows.addLine(to: CGPoint(x: 9.5, y: 18.5))
            ctx.addPath(arrows)
            ctx.strokePath()
            return true
        }
        img.isTemplate = true
        return img
    }
}
