// Renders packaging/macos/AppIcon.png (1024x1024) from the web UI's favicon
// mark (internal/control/ui/index.html): a green octagon crossed by two
// opposing arrows, centred on a dark macOS-style rounded square.
//
//   xcrun swift packaging/macos/render-icon.swift packaging/macos/AppIcon.png
import AppKit

let outPath = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "AppIcon.png"
let canvas: CGFloat = 1024

func rgb(_ hex: UInt32, _ a: CGFloat = 1) -> CGColor {
    CGColor(srgbRed: CGFloat((hex >> 16) & 0xff) / 255, green: CGFloat((hex >> 8) & 0xff) / 255,
            blue: CGFloat(hex & 0xff) / 255, alpha: a)
}

guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(canvas), pixelsHigh: Int(canvas),
                                 bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                 colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0),
      let gctx = NSGraphicsContext(bitmapImageRep: rep) else { fatalError("no bitmap context") }
let ctx = gctx.cgContext
ctx.clear(CGRect(x: 0, y: 0, width: canvas, height: canvas))
// Work in SVG-style coordinates (origin top-left, y down).
ctx.translateBy(x: 0, y: canvas)
ctx.scaleBy(x: 1, y: -1)

// Apple's macOS icon grid: an 824pt tile centred on the 1024 canvas (~80%),
// corner radius ~185, with a soft drop shadow.
let tile = CGRect(x: 100, y: 100, width: 824, height: 824)
let tilePath = CGPath(roundedRect: tile, cornerWidth: 185, cornerHeight: 185, transform: nil)
ctx.saveGState()
ctx.setShadow(offset: CGSize(width: 0, height: 12), blur: 28, color: rgb(0x000000, 0.45))
ctx.addPath(tilePath)
ctx.setFillColor(rgb(0x111318))
ctx.fillPath()
ctx.restoreGState()

// Subtle vertical gradient and a hairline rim so the tile reads on dark Docks.
ctx.saveGState()
ctx.addPath(tilePath)
ctx.clip()
let gradient = CGGradient(colorsSpace: CGColorSpace(name: CGColorSpace.sRGB),
                          colors: [rgb(0x1f242c), rgb(0x0b0d11)] as CFArray, locations: [0, 1])!
ctx.drawLinearGradient(gradient, start: CGPoint(x: 0, y: tile.minY), end: CGPoint(x: 0, y: tile.maxY), options: [])
ctx.restoreGState()
ctx.addPath(CGPath(roundedRect: tile.insetBy(dx: 1.5, dy: 1.5), cornerWidth: 184, cornerHeight: 184, transform: nil))
ctx.setStrokeColor(rgb(0xffffff, 0.08))
ctx.setLineWidth(3)
ctx.strokePath()

// The mark: 24-unit viewBox scaled to ~56% of the tile.
let markSize: CGFloat = 460
ctx.translateBy(x: (canvas - markSize) / 2, y: (canvas - markSize) / 2)
ctx.scaleBy(x: markSize / 24, y: markSize / 24)

let octagon = CGMutablePath()
octagon.addLines(between: [
    CGPoint(x: 6, y: 0), CGPoint(x: 18, y: 0), CGPoint(x: 24, y: 6), CGPoint(x: 24, y: 18),
    CGPoint(x: 18, y: 24), CGPoint(x: 6, y: 24), CGPoint(x: 0, y: 18), CGPoint(x: 0, y: 6),
])
octagon.closeSubpath()
ctx.saveGState()
ctx.setShadow(offset: .zero, blur: 2.2, color: rgb(0x00e08a, 0.35))
ctx.addPath(octagon)
ctx.setFillColor(rgb(0x00e08a))
ctx.fillPath()
ctx.restoreGState()

let arrows = CGMutablePath()
arrows.move(to: CGPoint(x: 5, y: 8.5)); arrows.addLine(to: CGPoint(x: 17.5, y: 8.5)); arrows.addLine(to: CGPoint(x: 14.5, y: 5.5))
arrows.move(to: CGPoint(x: 19, y: 15.5)); arrows.addLine(to: CGPoint(x: 6.5, y: 15.5)); arrows.addLine(to: CGPoint(x: 9.5, y: 18.5))
arrows.move(to: CGPoint(x: 12, y: 12)); arrows.addLine(to: CGPoint(x: 12.01, y: 12))
ctx.addPath(arrows)
ctx.setStrokeColor(rgb(0x06130d))
ctx.setLineWidth(2.2)
ctx.setLineCap(.square)
ctx.setLineJoin(.miter)
ctx.strokePath()

gctx.flushGraphics()
guard let png = rep.representation(using: .png, properties: [:]) else { fatalError("png encode failed") }
try png.write(to: URL(fileURLWithPath: outPath))
print("wrote \(outPath)")
