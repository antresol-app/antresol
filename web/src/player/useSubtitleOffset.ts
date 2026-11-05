// «Субтитры раньше / позже» of the file that plays: a press shows at once;
// the value is saved for the profile SAVE_MS after the last press, or at
// once when the part changes or the player closes with one still waiting.
// A failed save puts back what the server has.
import { useCallback, useEffect, useRef, useState } from 'react'
import { OFFSET_STEP_MS } from './lrc'

/** The server keeps an offset within ±60 s. */
export const SUBTITLE_OFFSET_MAX_MS = 60_000
const SAVE_MS = 800

/** `ms` on the step grid, within ±SUBTITLE_OFFSET_MAX_MS. */
export function clampSubtitleOffset(ms: number): number {
  const v = Math.round(ms / OFFSET_STEP_MS) * OFFSET_STEP_MS
  return Math.max(-SUBTITLE_OFFSET_MAX_MS, Math.min(SUBTITLE_OFFSET_MAX_MS, v)) || 0
}

type Save = (fileId: number, ms: number) => Promise<number>

/**
 * The offset of `fileId` (starting at `initial`, what the server had when
 * the player opened) and a setter. Without `save` (a stream) it lives only
 * as long as the player.
 */
export function useSubtitleOffset(fileId: number, initial: number, save?: Save): [number, (ms: number) => void] {
  // per file: a part played again keeps what was set on it
  const [values, setValues] = useState<Record<number, number>>({})
  const ms = values[fileId] ?? initial
  const saved = useRef<Record<number, number>>({})
  const pending = useRef<{ fileId: number; ms: number; timer: ReturnType<typeof setTimeout> } | null>(null)
  const alive = useRef(true)
  const saveRef = useRef(save)
  saveRef.current = save

  const send = useCallback((file: number, v: number) => {
    const s = saveRef.current
    if (!s) return
    s(file, v).then(
      (kept) => {
        saved.current[file] = kept
        if (alive.current && pending.current?.fileId !== file && kept !== v) setValues((x) => ({ ...x, [file]: kept }))
      },
      () => {
        if (alive.current && pending.current?.fileId !== file && file in saved.current) setValues((x) => ({ ...x, [file]: saved.current[file]! }))
      },
    )
  }, [])

  const flush = useCallback(() => {
    const p = pending.current
    if (!p) return
    clearTimeout(p.timer)
    pending.current = null
    if (p.ms !== saved.current[p.fileId]) send(p.fileId, p.ms)
  }, [send])

  // a change still waiting goes out when the part changes or the player closes
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
      flush()
    }
  }, [flush])
  useEffect(() => flush, [fileId, flush])

  const move = useCallback(
    (next: number) => {
      if (!(fileId in saved.current)) saved.current[fileId] = ms
      if (next === ms) return
      setValues((x) => ({ ...x, [fileId]: next }))
      if (pending.current) clearTimeout(pending.current.timer)
      const file = fileId
      const timer = setTimeout(() => {
        pending.current = null
        if (next !== saved.current[file]) send(file, next)
      }, SAVE_MS)
      pending.current = { fileId: file, ms: next, timer }
    },
    [fileId, ms, send],
  )
  return [ms, move]
}
