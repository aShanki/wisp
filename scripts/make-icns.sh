#!/usr/bin/env bash
# Rebuild packaging/Wisp.icns, the default macOS app icon, from
# packaging/wisp-macos.svg. macOS only (Swift, sips, iconutil).
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cat > "$tmp/render.swift" <<'SWIFT'
import AppKit
let args = CommandLine.arguments
guard let image = NSImage(contentsOf: URL(fileURLWithPath: args[1])) else { fatalError("cannot load \(args[1])") }
let size = 1024
let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size, bitsPerSample: 8,
    samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
image.draw(in: NSRect(x: 0, y: 0, width: size, height: size))
NSGraphicsContext.restoreGraphicsState()
try rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: args[2]))
SWIFT
swift "$tmp/render.swift" packaging/wisp-macos.svg "$tmp/1024.png"
set="$tmp/Wisp.iconset"
mkdir "$set"
for size in 16 32 128 256 512; do
  sips -z $size $size "$tmp/1024.png" --out "$set/icon_${size}x${size}.png" >/dev/null
  sips -z $((size * 2)) $((size * 2)) "$tmp/1024.png" --out "$set/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns -o packaging/Wisp.icns "$set"
echo "packaging/Wisp.icns"
