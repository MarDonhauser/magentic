export const HYDRA_MAX_TILES = 12;
export const HYDRA_MIN_TILE_WIDTH = 460;

export function hydraColumns(count, width, minTile = HYDRA_MIN_TILE_WIDTH, gap = 6) {
  const tiles = Math.max(0, Math.floor(Number(count) || 0));
  if (tiles <= 1) return 1;
  const available = Math.max(0, Number(width) || 0);
  const fit = Math.max(1, Math.floor((available + gap) / (minTile + gap)));
  const wanted = tiles <= 4 ? 2 : tiles <= 9 ? 3 : 4;
  return Math.min(wanted, fit);
}
