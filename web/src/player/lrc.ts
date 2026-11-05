// LRC lyrics: «[mm:ss.xx]line» time tags (several per line allowed), an
// optional «[offset:±ms]» (positive shows the lines sooner). Other «[xx:…]»
// tags (artist, title) are ignored.

export interface LrcLine {
  timeSec: number
  line: string
}

const TIME = /\[(\d{1,3}):(\d{1,2})(?:[.:](\d{1,3}))?\]/g
const OFFSET = /^\s*\[offset:\s*([+-]?\d+)\s*\]\s*$/im

/** Lines sorted by time, or null when the text has no time tags (plain lyrics). */
export function parseLrc(text: string): LrcLine[] | null {
  const out: LrcLine[] = []
  const off = OFFSET.exec(text)
  const shift = off ? Number(off[1]) / 1000 : 0
  for (const raw of text.split(/\r?\n/)) {
    TIME.lastIndex = 0
    const times: number[] = []
    let rest = raw.trim()
    // the tags sit at the start of the line, before the words
    for (;;) {
      TIME.lastIndex = 0
      const m = TIME.exec(rest)
      if (!m || m.index !== 0) break
      const frac = m[3] ? Number(m[3]) / 10 ** m[3].length : 0
      times.push(Number(m[1]) * 60 + Number(m[2]) + frac)
      rest = rest.slice(m[0].length)
    }
    for (const t of times) out.push({ timeSec: Math.max(0, Math.round((t - shift) * 1000) / 1000), line: rest.trim() })
  }
  if (out.length === 0) return null
  return out.sort((a, b) => a.timeSec - b.timeSec)
}

/** The per-track correction a «Раньше» / «Позже» press moves, ms. */
export const OFFSET_STEP_MS = 250
/** The server keeps the correction within ±10 s. */
export const OFFSET_MAX_MS = 10_000

/** Whole `ms` kept within ±OFFSET_MAX_MS (a stored value off the step stays as it is). */
export function clampOffset(ms: number): number {
  return Math.max(-OFFSET_MAX_MS, Math.min(OFFSET_MAX_MS, Math.round(ms))) || 0
}

/**
 * The lines with the track's correction added (positive: later; on top of
 * the LRC's own [offset:] tag, already in the times). Never before 0.
 */
export function shiftLines(lines: LrcLine[], offsetMs: number): LrcLine[] {
  if (!offsetMs) return lines
  return lines.map((l) => ({ ...l, timeSec: Math.max(0, Math.round(l.timeSec * 1000 + offsetMs) / 1000) }))
}

/** «+0,50» / "-1.25": seconds with two decimals and a sign («0,00» at zero). */
export function offsetSeconds(ms: number, locale: string): string {
  return new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2, signDisplay: 'exceptZero' }).format(ms / 1000)
}

/** Index of the last line whose time is not after `sec`; -1 before the first. */
export function activeLine(lines: LrcLine[], sec: number): number {
  let lo = 0
  let hi = lines.length - 1
  let ans = -1
  while (lo <= hi) {
    const mid = (lo + hi) >> 1
    if (lines[mid]!.timeSec <= sec) {
      ans = mid
      lo = mid + 1
    } else hi = mid - 1
  }
  return ans
}
