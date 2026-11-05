// Scrub previews (GET /api/files/{id}/trickplay): which tile of which
// sheet shows a time, and the CSS that crops it out of the sheet.
import { useEffect, useState } from 'react'
import { getTrickplay, trickplaySheetUrl } from '../api/client'
import type { TrickplayIndex } from '../api/types'

export interface TrickplayTile {
  sheet: number
  /** Column and row in the sheet. */
  col: number
  row: number
}

/** The tile showing `sec` on the file timeline (the last one past the end); null without frames. */
export function tileAt(ix: TrickplayIndex, sec: number): TrickplayTile | null {
  if (ix.count <= 0 || ix.interval <= 0 || ix.columns <= 0 || ix.rows <= 0) return null
  const i = Math.min(Math.max(Math.floor(sec / ix.interval), 0), ix.count - 1)
  const per = ix.columns * ix.rows
  const j = i % per
  return { sheet: Math.floor(i / per), col: j % ix.columns, row: Math.floor(j / ix.columns) }
}

/** The background that shows `tile` in a box `width` px wide (the height follows the frame's). */
export function tileStyle(ix: TrickplayIndex, tile: TrickplayTile, url: string, width: number) {
  const scale = width / ix.width
  const w = ix.width * scale
  const h = ix.height * scale
  return {
    width: `${Math.round(w)}px`,
    height: `${Math.round(h)}px`,
    backgroundImage: `url("${url}")`,
    backgroundSize: `${ix.columns * w}px ${ix.rows * h}px`,
    backgroundPosition: `${-tile.col * w}px ${-tile.row * h}px`,
  }
}

/** What the Scrubber needs to draw a preview. */
export interface ScrubPreview {
  index: TrickplayIndex
  sheetUrl: (n: number) => string
}

/** Sheets already asked for, by URL (the browser's cache keeps them). */
const preloaded = new Set<string>()

/** Loads sheet `n` of the previews and the one after it (once each). */
export function preloadSheets(p: ScrubPreview, n: number) {
  for (const s of [n, n + 1]) {
    if (s < 0 || s >= p.index.sheets) continue
    const url = p.sheetUrl(s)
    if (preloaded.has(url)) continue
    preloaded.add(url)
    if (preloaded.size > 200) preloaded.delete(preloaded.values().next().value!)
    const img = new Image()
    img.decoding = 'async'
    img.src = url
  }
}

/**
 * The scrub previews of a library file, once the server has made them
 * (undefined until then, for an online stream, or on an error: the
 * timeline then shows the time alone).
 */
export function useTrickplay(fileId: number | undefined): ScrubPreview | undefined {
  const [got, setGot] = useState<{ fileId: number; preview: ScrubPreview } | null>(null)
  useEffect(() => {
    if (!fileId || fileId <= 0) return
    const ac = new AbortController()
    getTrickplay(fileId, ac.signal)
      .then((ix) => {
        if (ac.signal.aborted || !ix || !ix.sheets) return
        setGot({ fileId, preview: { index: ix, sheetUrl: (n) => trickplaySheetUrl(fileId, ix, n) } })
      })
      .catch(() => {}) // no preview: the time alone
    return () => ac.abort()
  }, [fileId])
  return got && got.fileId === fileId ? got.preview : undefined
}
