// Page turns of the comic reader: keys and taps follow the reading
// direction. Right to left (manga): ← and a tap on the left third go
// forward; left to right: → and the right third. The middle third shows
// the panels.

export type ComicDir = 'rtl' | 'ltr'

/** +1 next page, −1 previous, 0 not a page key. */
export function nextPageKey(key: string, dir: ComicDir): -1 | 0 | 1 {
  switch (key) {
    case 'ArrowLeft':
      return dir === 'rtl' ? 1 : -1
    case 'ArrowRight':
      return dir === 'rtl' ? -1 : 1
    case ' ':
    case 'PageDown':
      return 1
    case 'PageUp':
      return -1
    default:
      return 0
  }
}

/** A tap at x on a stage `width` wide: the outer thirds turn, the middle is 0. */
export function tapZone(x: number, width: number, dir: ComicDir): -1 | 0 | 1 {
  if (!(width > 0)) return 0
  const left = x < width / 3
  const right = x > (width * 2) / 3
  if (!left && !right) return 0
  const forward = dir === 'rtl' ? left : right
  return forward ? 1 : -1
}
