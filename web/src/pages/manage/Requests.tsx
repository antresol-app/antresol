// «Управление → Запросы» (admin): the switch «Дети могут просить» (the server
// setting «Запросы детей», on by default, no addon needed), then what kid
// profiles asked for («Попросить»), the waiting ones first. «Одобрить» opens the sheet of the first addon
// request action that wants the request (requestActions: a release picker,
// whose download approves the request on the server); with none it
// approves at once, and a sheet that throws leaves the request waiting
// with an error shown. Without one
// the answer is the parent's promise: the title is added to the library by any
// means, and the kid sees «Уже в медиатеке» once the library has it. «Отказать» takes an
// optional reason the kid sees. Updates live (request.changed, library.updated).
import { useCallback, useEffect, useRef, useState } from 'react'
import { approveRequest, declineRequest, getRequests, getSettings, setKidRequests, useEvents, errorText } from '../../api/client'
import type { KidRequest } from '../../api/types'
import { Button } from '../../ui/Button'
import { Icon } from '../../components/Icon'
import { RequestStatus } from '../../components/RequestButton'
import { Sheet } from '../../ui/Sheet'
import { TextField } from '../../ui/TextField'
import { ago } from '../../state/format'
import { refreshPendingRequests } from '../../state/kidRequests'
import { AddonBoundary, useAddonSlot } from '../../addons/slots'
import type { Contribution } from '../../addons/registry'
import { useT } from '../../i18n'
import { RequestCard } from '../RequestCard'
import s from '../Search.module.css'
import { GroupedSection, ListRow } from '../../ui/List'
import { Spinner } from '../../ui/Spinner'
import { Toggle } from '../../ui/Toggle'
import m from './Manage.module.css'

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; list: KidRequest[] }

type Action = Contribution<'requestActions'>

/** The first action that wants the request (a `visible` that throws: not that one). */
function actionFor(actions: Action[], r: KidRequest): Action | undefined {
  return actions.find((a) => {
    try {
      return !a.visible || a.visible(r)
    } catch (e) {
      console.warn(`antresol: addon ${a.addonId} failed to answer for a request`, e)
      return false
    }
  })
}

/** «Дети могут просить»: the server's «Запросы детей» (an older server has none: the row is left out). */
function RequestsSwitch() {
  const t = useT()
  const [on, setOn] = useState<boolean | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    const ac = new AbortController()
    getSettings(ac.signal).then(
      (v) => setOn(v.kidRequests ?? null),
      () => undefined, // the list below says what failed
    )
    return () => ac.abort()
  }, [])
  if (on === null) return null
  const change = (next: boolean) => {
    setOn(next)
    setError('')
    setKidRequests(next).then(
      (v) => setOn(v.kidRequests ?? next),
      (e: unknown) => {
        setOn(!next)
        setError(errorText(e, t('settings.saveFailed')))
      },
    )
  }
  return (
    <>
      {error && (
        <div className={m.problem} role="alert">
          <Icon name="info" size={18} /> {error}
        </div>
      )}
      <GroupedSection>
        <ListRow label={t('requests.switch')} hint={t('requests.switch.hint')}>
          <Toggle label={t('requests.switch')} checked={on} onChange={change} />
        </ListRow>
      </GroupedSection>
    </>
  )
}

export function Requests() {
  const t = useT()
  const actions = useAddonSlot('requestActions')
  const [state, setState] = useState<State>({ status: 'loading' })
  const [approving, setApproving] = useState<{ request: KidRequest; action: Action } | null>(null)
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
    const action = actionFor(actions, r)
    if (action) setApproving({ request: r, action })
    else approveNow(r)
  }
  const approved = () => {
    setApproving(null)
    load(true)
    refreshPendingRequests()
  }
  const line = (r: KidRequest) => t('requests.from', { name: r.user?.name ?? '', when: ago(r.createdAt) })

  return (
    <section aria-labelledby="requests-title">
      <header className={m.header}>
        <div>
          <h2 id="requests-title" className={m.title}>
            {t('requests.title')}
          </h2>
          <p className={m.subtitle}>{t('requests.lead')}</p>
        </div>
      </header>
      <RequestsSwitch />
      {state.status === 'loading' && (
        <div className={m.spinnerRow} role="status">
          <Spinner track className={m.spinner} /> {t('downloads.loading')}
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
      {approving && (
        <AddonBoundary
          key={approving.request.id}
          addonId={approving.action.addonId}
          fallback={null}
          // the sheet failed: the request stays waiting (approving now would
          // say «Одобрено» with nothing coming) and the admin is told
          onError={() => {
            setApproving(null)
            setError(t('requests.sheetFailed'))
          }}
        >
          <approving.action.element request={approving.request} onClose={() => setApproving(null)} onApproved={approved} />
        </AddonBoundary>
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
