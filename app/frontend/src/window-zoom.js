export function isDragRegion(el, styleOf) {
  return Boolean(el) && styleOf(el).getPropertyValue('--wails-draggable').trim() === 'drag';
}

export function installDragRegionZoom(target, styleOf, toggle) {
  // Wails startet beim ersten Klick performWindowDrag, der das mouseup schluckt —
  // ein dblclick-Event käme so nicht zuverlässig an, der zweite mousedown schon.
  target.addEventListener('mousedown', (e) => {
    if (e.button === 0 && e.detail === 2 && isDragRegion(e.target, styleOf)) toggle();
  });
}
