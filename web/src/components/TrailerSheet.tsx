// A title's trailer, played inside Antresol: a large sheet with the video.
// With `play` the server has (or fetches) the trailer, and it plays in a
// <video> from the server: a spinner while it is prepared (a busy server is
// asked again after its Retry-After, for up to half a minute), the error and
// «Повторить» when it fails. Without `play` (a server that does not fetch
// trailers) the official embedded YouTube player plays it in the same place.
import { useEffect, useState } from 'react'
import { errorText, playTrailer } from '../api/client'
import type { Trailer } from '../api/types'
import { audio } from '../player/audio/engine'
import { Button } from '../ui/Button'
import { Sheet } from '../ui/Sheet'
import { useT } from '../i18n'
import styles from './TrailerSheet.module.css'

/** How long a busy server is asked again before the sheet gives up. */
const BUSY_GIVE_UP_MS = 30_000

/** The official embedded player of a trailer the server does not play. */
export const trailerEmbedUrl = (key: string) =>
  `https://www.youtube-nocookie.com/embed/${encodeURIComponent(key)}?autoplay=1&playsinline=1&rel=0`

type State = { kind: 'loading' } | { kind: 'ready'; url: string } | { kind: 'error'; message: string }

export interface TrailerSheetProps {
  trailer: Trailer
  onClose: () => void
}

export function TrailerSheet({ trailer, onClose }: TrailerSheetProps) {
  const t = useT()
  const [state, setState] = useState<State>({ kind: 'loading' })
  const [attempt, setAttempt] = useState(0)
  const play = trailer.play

  // the music steps aside, as for any video
  useEffect(() => {
    audio.yieldSession()
  }, [])

  useEffect(() => {
    if (!play) return
    const ac = new AbortController()
    const started = Date.now()
    let timer: ReturnType<typeof setTimeout> | undefined
    setState({ kind: 'loading' })
    const ask = () => {
      playTrailer(play, ac.signal).then(
        (r) => {
          if (ac.signal.aborted) return
          if ('wait' in r) {
            if (Date.now() - started + r.wait * 1000 > BUSY_GIVE_UP_MS) setState({ kind: 'error', message: t('comp.trailer.busy') })
            else timer = setTimeout(ask, r.wait * 1000)
            return
          }
          setState({ kind: 'ready', url: r.url })
        },
        (e: unknown) => {
          if (!ac.signal.aborted) setState({ kind: 'error', message: errorText(e, t('comp.trailer.failed')) })
        },
      )
    }
    ask()
    return () => {
      ac.abort()
      clearTimeout(timer)
    }
  }, [play, attempt])

  const title = trailer.name || t('comp.action.trailer')
  let body
  if (!play) {
    body = (
      <iframe
        className={styles.media}
        src={trailerEmbedUrl(trailer.key)}
        title={title}
        allow="autoplay; encrypted-media; picture-in-picture; fullscreen"
        allowFullScreen
        referrerPolicy="strict-origin-when-cross-origin"
      />
    )
  } else if (state.kind === 'ready') {
    body = (
      <video
        key={state.url}
        className={styles.media}
        src={state.url}
        controls
        autoPlay
        playsInline
        data-autofocus
        onError={() => setState({ kind: 'error', message: t('comp.trailer.cannotPlay') })}
      />
    )
  } else if (state.kind === 'error') {
    body = (
      <div className={styles.status} role="alert">
        <p className={styles.message}>{state.message}</p>
        <Button size="small" variant="secondary" onClick={() => setAttempt((n) => n + 1)}>
          {t('common.retry')}
        </Button>
      </div>
    )
  } else {
    body = (
      <div className={styles.status} role="status">
        <span className={styles.spinner} aria-hidden />
        <p className={styles.message}>{t('comp.trailer.preparing')}</p>
      </div>
    )
  }

  return (
    <Sheet title={title} role="dialog" size="large" noButtons closeButton onCancel={onClose}>
      <div className={styles.frame} data-testid="trailer-frame">
        {body}
      </div>
    </Sheet>
  )
}
