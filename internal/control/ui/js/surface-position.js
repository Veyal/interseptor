// Fit a floating menu inside the visual viewport and prefer the roomy side.
export function placeFloatingSurface(anchor, desiredWidth, desiredHeight, viewport) {
  const margin = 8, gap = 6;
  const x = viewport.left || 0, y = viewport.top || 0;
  const width = Math.min(desiredWidth, Math.max(0, viewport.width - margin * 2));
  const minTop = y + margin, maxBottom = y + viewport.height - margin;
  const anchorTop = Math.max(minTop, Math.min(anchor.top, maxBottom));
  const anchorBottom = Math.max(minTop, Math.min(anchor.bottom, maxBottom));
  const below = Math.max(0, maxBottom - anchorBottom - gap);
  const above = Math.max(0, anchorTop - minTop - gap);
  const left = Math.max(x + margin, Math.min(anchor.left, x + viewport.width - width - margin));
  // Taller than the room on either side but not than the viewport: open at the
  // anchor and shift up just enough to show every row, rather than flipping to the
  // far edge with a clipped, scrolling list.
  if (desiredHeight > below && desiredHeight > above && desiredHeight <= maxBottom - minTop) {
    return { left, top: Math.max(minTop, Math.min(anchorBottom + gap, maxBottom - desiredHeight)), width, maxHeight: desiredHeight, side: 'below' };
  }
  const side = below < desiredHeight && above > below ? 'above' : 'below';
  const maxHeight = Math.min(desiredHeight, side === 'above' ? above : below);
  const targetTop = side === 'above' ? anchorTop - gap - maxHeight : anchorBottom + gap;
  const top = Math.max(minTop, Math.min(targetTop, maxBottom - maxHeight));
  return { left, top, width, maxHeight, side };
}
