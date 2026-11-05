// «Попросить»: what a kid profile has where an adult has «Смотреть ▾», the
// download button or «На сервер» — on a search result and on a TMDB title
// page (another build's). The button asks once; then
// the title shows the request's state: «Ждёт ответа», «Одобрено — скоро
// появится», «Уже в медиатеке» (a link to it) or «Родитель отказал» with
// the reason. Above the profile's age limit it stays disabled and says why.
import { useId, useState } from 'react'
import { ApiError, askRequest, type KidRequestAsk } from '../api/client'
import type { KidRequest, KidRequestState } from '../api/types'
import { useT } from '../i18n'
import { requestKey, useMyRequests } from '../state/kidRequests'
import { useSession } from '../state/session'
import { Button, ButtonLink } from '../ui/Button'
import { cardHref } from './Card'
import { Icon } from './Icon'
import styles from './RequestButton.module.css'

/** The page of a TMDB title that is not in the library (the sources build's route). */
export const titlePath = (c: { kind: string; tmdbId: number }) => `/title/${c.kind}/${c.tmdbId}`

/** The library page of a requested title that is in the library now. */
export function requestHref(r: Pick<KidRequest, 'kind' | 'libraryItemId' | 'title'>): string | undefined {
  if (r.libraryItemId == null) return undefined
  const kind = r.kind === 'music' ? 'album' : r.kind
  return cardHref({ id: r.libraryItemId, kind, title: r.title, subtitle: '', poster: '', backdrop: '', logo: '', progress: 0, onServer: true })
}

/** A request's state as a line («Ждёт ответа», …); the reason of a refusal under it. */
export function RequestStatus({ request, className }: { request: KidRequest; className?: string }) {
  const t = useT()
  const href = request.status === 'in_library' ? requestHref(request) : undefined
  if (href)
    return (
      <ButtonLink to={href} size="small" variant="secondary" icon="checkCircle" className={className}>
        {t('requests.inLibrary')}
      </ButtonLink>
    )
  const icon = request.status === 'pending' ? 'clock' : request.status === 'declined' ? 'info' : 'checkCircle'
  const label =
    request.status === 'pending' ? t('requests.pending') : request.status === 'declined' ? t('requests.declined') : request.status === 'approved' ? t('requests.approved') : t('requests.inLibrary')
  return (
    <div className={[styles.status, className].filter(Boolean).join(' ')} data-status={request.status}>
      <span className={styles.pill}>
        <Icon name={icon} size={16} />
        {label}
      </span>
      {request.status === 'declined' && request.reason && <span className={styles.reason}>{t('requests.reasonLine', { reason: request.reason })}</span>}
    </div>
  )
}

export function RequestButton({ ask, state, size = 'small' }: { ask: KidRequestAsk; state: KidRequestState; size?: 'small' | 'large' }) {
  const t = useT()
  const { user } = useSession()
  const { requests, put } = useMyRequests(user?.id ?? 0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // a movie of the search results above the limit: known once asked for
  const [blocked, setBlocked] = useState('')
  const noteId = useId()
  const request = requests.get(requestKey(ask.kind, state.key)) ?? state.request
  const why = blocked || (state.allowed ? '' : (state.blocked ?? ''))

  if (request)
    return (
      <div role="status">
        <RequestStatus request={request} />
      </div>
    )

  const send = () => {
    setBusy(true)
    setError('')
    askRequest(ask)
      .then(put, (e: unknown) => {
        if (e instanceof ApiError && e.code === 'rating') setBlocked(e.message)
        else setError(e instanceof ApiError ? e.message : t('requests.askFailed'))
      })
      .finally(() => setBusy(false))
  }
  return (
    <div className={styles.ask}>
      <Button
        size={size}
        variant={size === 'large' ? 'primary' : 'secondary'}
        icon="send"
        loading={busy}
        disabled={busy || !!why}
        aria-label={t('requests.askFor', { title: ask.title })}
        aria-describedby={why ? noteId : undefined}
        onClick={send}
      >
        {t('requests.ask')}
      </Button>
      {why && (
        <p id={noteId} className={styles.note} aria-live="polite">
          {why}
        </p>
      )}
      {error && (
        <p className={styles.error} role="alert">
          {error}
        </p>
      )}
    </div>
  )
}
