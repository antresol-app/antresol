// Settings that save themselves, as in iOS: every change is sent (after
// `wait` ms, so a drag or typing is one request), «Готово» only closes.
// flush() sends what is still waiting (before the sheet closes); an unmount
// sends it too, so nothing changed is lost.
import { useEffect, useRef, useState } from 'react'

export type SaveStatus = '' | 'saving' | 'saved'

export function useAutosave<T>(save: (value: T) => Promise<unknown>, onError: (e: unknown) => void) {
  const [status, setStatus] = useState<SaveStatus>('')
  const timer = useRef<number | undefined>(undefined)
  const pending = useRef<{ value: T } | null>(null)
  const alive = useRef(true)
  const fns = useRef({ save, onError })
  fns.current = { save, onError }

  const run = async () => {
    const p = pending.current
    pending.current = null
    if (!p) return
    if (alive.current) setStatus('saving')
    try {
      await fns.current.save(p.value)
      if (alive.current) setStatus('saved')
    } catch (e) {
      if (alive.current) {
        setStatus('')
        fns.current.onError(e)
      }
    }
  }

  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
      window.clearTimeout(timer.current)
      if (pending.current) void run()
    }
    // run reads refs only
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return {
    status,
    schedule(value: T, wait = 0) {
      pending.current = { value }
      window.clearTimeout(timer.current)
      timer.current = window.setTimeout(() => void run(), wait)
    },
    async flush() {
      window.clearTimeout(timer.current)
      await run()
    },
  }
}
