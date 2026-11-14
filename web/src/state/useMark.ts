// The profile's mark on one title (tracking): loaded once, set and cleared
// through the server; the shown state follows the server's answer.
import { useCallback, useEffect, useState } from 'react'
import { deleteMark, getMark, putMark } from '../api/client'
import type { MarkState, TitleMark } from '../api/types'

/** `title`: the name and poster sent with a new mark of a title off the library. */
export function useMark(kind: 'series' | 'movie', tmdbId: number, title?: { name?: string; poster?: string }) {
  const [mark, setMark] = useState<TitleMark | null>(null)
  const [failed, setFailed] = useState(false)
  const name = title?.name
  const poster = title?.poster

  useEffect(() => {
    const ac = new AbortController()
    setMark(null)
    getMark(kind, tmdbId, ac.signal)
      .then(setMark)
      .catch(() => undefined) // no mark shown; choosing one still works
    return () => ac.abort()
  }, [kind, tmdbId])

  const set = useCallback(
    (state: MarkState) => {
      setFailed(false)
      putMark(kind, tmdbId, state, { name, poster }).then(setMark, () => setFailed(true))
    },
    [kind, tmdbId, name, poster],
  )
  const clear = useCallback(() => {
    setFailed(false)
    deleteMark(kind, tmdbId).then(() => setMark(null), () => setFailed(true))
  }, [kind, tmdbId])

  return { mark, failed, set, clear }
}
