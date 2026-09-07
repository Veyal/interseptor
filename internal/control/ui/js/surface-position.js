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
  const side = below < desiredHeight && above > below ? 'above' : 'below';
  const maxHeight = Math.min(desiredHeight, side === 'above' ? above : below);
  const left = Math.max(x + margin, Math.min(anchor.left, x + viewport.width - width - margin));
  const targetTop = side === 'above' ? anchorTop - gap - maxHeight : anchorBottom + gap;
  const top = Math.max(minTop, Math.min(targetTop, maxBottom - maxHeight));
  return { left, top, width, maxHeight, side };
}
