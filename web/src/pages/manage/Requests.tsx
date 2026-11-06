// «Управление → Просьбы» (admin): what kid profiles asked for («Попросить»),
// the waiting ones first. «Одобрить» approves a request at once; on a server
// that can download (user.canAcquire) it opens the release picker of the
// title instead (a series: its page, where seasons are picked), and the
// download it starts approves the request on the server. The kid sees
// «Уже в медиатеке» once the library has the title. «Отказать» takes an
// optional reason the kid sees. Updates live (request.changed, library.updated).
import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { approveRequest, declineRequest, getRequests, useEvents, errorText } from '../../api/client'
import type { KidRequest } from '../../api/types'
import { Button } from '../../ui/Button'
import { Icon } from '../../components/Icon'
import { RequestStatus, titlePath } from '../../components/RequestButton'
import { Sheet } from '../../ui/Sheet'
import { TextField } from '../../ui/TextField'
import { ago } from '../../state/format'
import { refreshPendingRequests } from '../../state/kidRequests'
import { useSession } from '../../state/session'
import { useT } from '../../i18n'
import { RequestCard } from '../RequestCard'
import s from '../Search.module.css'
import m from './Manage.module.css'

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; list: KidRequest[] }

// The release picker of the sources build (a lazy chunk; none in the core build)
const ReleasePicker = __LIBRARY_EDITION__ ? null : lazy(() => import('./RequestsRelease'))

export function Requests() {
  const t = useT()
  const navigate = useNavigate()
  const { user } = useSession()
  // a server that downloads: «Одобрить» picks the release; else it approves at once
  const canAcquire = !__LIBRARY_EDITION__ && Boolean(user?.canAcquire)
  const [state, setState] = useState<State>({ status: 'loading' })
  const [approving, setApproving] = useState<KidRequest | null>(null)
  const [declining, setDeclining] = useState<KidRequest | null>(null)
  const [busy, setBusy] = useState<number | null>(null)
  const [error, setError] = useState('')
  const abort = useRef<AbortController | null>(null)

  const load = useCallback((quiet = false) => {
    abort.current?.abort()
    const ac = new AbortController()
    abort.current = ac
    if (!quiet) setState({ status: 'loading' })
    getRequests(ac.signal).then(
      (list) => setState({ status: 'ready', list }),
      (e: unknown) => {
        if (ac.signal.aborted || quiet) return
        setState({ status: 'error', message: errorText(e, t('common.loadFailed')) })
      },
    )
  }, [t])
  useEffect(() => {
    load()
    return () => abort.current?.abort()
  }, [load])
  useEvents(() => load(true), ['request.changed', 'library.updated'])

  const list = state.status === 'ready' ? state.list : []
  const waiting = list.filter((r) => r.status === 'pending')
  const answered = list.filter((r) => r.status !== 'pending')
  const approveNow = (r: KidRequest) => {
    setBusy(r.id)
    setError('')
    approveRequest(r.id)
      .then(() => {
        load(true)
        refreshPendingRequests()
      })
      .catch((e: unknown) => setError(errorText(e, t('requests.approveFailed'))))
      .finally(() => setBusy(null))
  }
  const approve = (r: KidRequest) => {
    if (!canAcquire) approveNow(r)
    else if (r.kind === 'series') navigate(titlePath(r))
    else setApproving(r)
  }
  const line = (r: KidRequest) => t('requests.from', { name: r.user?.name ?? '', when: ago(r.createdAt) })

  return (
    <section aria-labelledby="requests-title">
      <header className={m.header}>
        <div>
          <h2 id="requests-title" className={m.title}>
            {t('requests.title')}
          </h2>
          <p className={m.subtitle}>{canAcquire ? t('requests.leadFull') : t('requests.lead')}</p>
        </div>
      </header>
      {state.status === 'loading' && (
        <div className={m.spinnerRow} role="status">
          <span className={m.spinner} aria-hidden /> {t('downloads.loading')}
        </div>
      )}
      {state.status === 'error' && (
        <div className={m.problem} role="alert">
          <Icon name="cloud" size={18} /> {state.message}
          <Button size="small" variant="secondary" onClick={() => load()}>
            {t('common.retry')}
          </Button>
        </div>
      )}
      {error && (
        <div className={m.problem} role="alert">
          <Icon name="info" size={18} /> {error}
        </div>
      )}
      {state.status === 'ready' && (
        <>
          <h3 className={m.groupTitle}>{t('requests.waiting')}</h3>
          {waiting.length === 0 ? (
            <div className={m.empty}>
              <Icon name="checkCircle" size={40} strokeWidth={1.4} />
              <p>{t('requests.empty')}</p>
            </div>
          ) : (
            <ul className={s.candidates} aria-label={t('requests.waiting')}>
              {waiting.map((r) => (
                <RequestCard key={r.id} request={r} line={line(r)}>
                  <div className={s.buttons}>
                    <Button
                      size="small"
                      icon="check"
                      loading={busy === r.id}
                      disabled={busy === r.id}
                      aria-label={t('requests.approveFor', { title: r.title })}
                      onClick={() => approve(r)}
                    >
                      {t('requests.approve')}
                    </Button>
                    <Button size="small" variant="secondary" aria-label={t('requests.declineFor', { title: r.title })} onClick={() => setDeclining(r)}>
                      {t('requests.decline')}
                    </Button>
                  </div>
                </RequestCard>
              ))}
            </ul>
          )}
          {answered.length > 0 && (
            <>
              <h3 className={m.groupTitle}>{t('requests.decided')}</h3>
              <ul className={s.candidates} aria-label={t('requests.decided')}>
                {answered.map((r) => (
                  <RequestCard key={r.id} request={r} line={line(r)}>
                    <div className={s.buttons}>
                      <AnsweredStatus request={r} />
                    </div>
                  </RequestCard>
                ))}
              </ul>
            </>
          )}
        </>
      )}
      {approving && ReleasePicker && (
        <Suspense fallback={null}>
          <ReleasePicker request={approving} onClose={() => setApproving(null)} />
        </Suspense>
      )}
      {declining && (
        <DeclineSheet
          request={declining}
          onClose={() => setDeclining(null)}
          onDone={() => {
            setDeclining(null)
            load(true)
            refreshPendingRequests()
          }}
        />
      )}
    </section>
  )
}

/** An answered request in the admin's words: «Одобрено», «Отказано», «Уже в медиатеке». */
function AnsweredStatus({ request: r }: { request: KidRequest }) {
  const t = useT()
  if (r.status === 'in_library') return <RequestStatus request={r} />
  return (
    <div>
      <p className={s.byline}>{r.status === 'declined' ? t('requests.admin.declined') : t('requests.admin.approved')}</p>
      {r.status === 'declined' && r.reason && <p className={s.matched}>{t('requests.reasonLine', { reason: r.reason })}</p>}
    </div>
  )
}

/** «Отказать» with an optional reason. */
function DeclineSheet({ request, onClose, onDone }: { request: KidRequest; onClose: () => void; onDone: () => void }) {
  const t = useT()
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const decline = () => {
    setBusy(true)
    setError('')
    declineRequest(request.id, reason.trim()).then(onDone, (e: unknown) => {
      setBusy(false)
      setError(errorText(e, t('requests.declineFailed')))
    })
  }
  return (
    <Sheet
      title={t('requests.declineTitle', { title: request.title })}
      message={t('requests.declineMessage', { name: request.user?.name ?? '' })}
      role="dialog"
      size="wide"
      busy={busy}
      onCancel={onClose}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: onClose },
        { label: t('requests.decline'), kind: 'destructive', disabled: busy, run: decline },
      ]}
    >
      <TextField label={t('requests.reason')} value={reason} maxLength={200} onChange={(e) => setReason(e.target.value)} error={error || null} />
    </Sheet>
  )
}
