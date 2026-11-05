// «Уведомления на почту»: a profile's address and what it wants to hear
// about by mail. Everything is off until the profile turns it on, and no
// letter goes before the address is confirmed («Отправить проверочное
// письмо»: a code to type here, and a link in the letter). An adult opens
// it from the profile menu; a kid's are the administrator's (Профили), as
// «Звук и субтитры» are. The letters come in the interface language the
// settings were saved in.
import { useEffect, useId, useState } from 'react'
import { ApiError, confirmNotifyCode, getNotifyPrefs, saveNotifyPrefs, sendNotifyVerification, type NotifyPrefsInput } from '../api/client'
import type { NotifyPrefs, NotifyTopic } from '../api/types'
import { getLanguage, useT, type Key } from '../i18n'
import { Button } from '../ui/Button'
import { Icon } from './Icon'
import { Sheet } from '../ui/Sheet'
import { TextField } from '../ui/TextField'
import { Toggle } from '../ui/Toggle'
import styles from './NotifySheet.module.css'

export interface NotifySheetProps {
  /** Another profile's (an administrator in «Профили»); none: the signed-in one. */
  userId?: number
  /** That profile's name, for the title. */
  name?: string
  onClose: () => void
}

// The labels of the topics. One is another build's (its strings
// are not in the core build's bundle).
const topicLabel = (topic: NotifyTopic): { label: Key; hint?: Key } | null => {
  switch (topic) {
    case 'episodes':
      return { label: 'comp.notify.episodes', hint: 'comp.notify.episodesHint' }
    case 'available':
      // the core build has no «Хочу посмотреть» of titles outside the
      // library: only one that went missing from the disk comes back
      return { label: 'comp.notify.available', hint: __LIBRARY_EDITION__ ? 'comp.notify.availableHint' : 'comp.notify.availableHintFull' }
    case 'requests':
      return { label: 'comp.notify.requests' }
    case 'downloads':
      return __LIBRARY_EDITION__ ? null : { label: 'comp.notify.downloads' }
  }
  return null
}

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; prefs: NotifyPrefs }

const inputOf = (p: NotifyPrefs): NotifyPrefsInput => ({
  email: p.email.trim(),
  episodes: p.episodes,
  available: p.available,
  requests: p.requests,
  downloads: p.downloads,
  quietHours: p.quietHours,
  lang: getLanguage(),
})

export function NotifySheet({ userId, name, onClose }: NotifySheetProps) {
  const t = useT()
  const id = useId()
  const [state, setState] = useState<State>({ status: 'loading' })
  // the address as saved on the server (a typed one is saved before its letter)
  const [savedEmail, setSavedEmail] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  const [codeSent, setCodeSent] = useState(false)
  const [code, setCode] = useState('')

  useEffect(() => {
    const ac = new AbortController()
    getNotifyPrefs(userId, ac.signal).then(
      (prefs) => {
        setState({ status: 'ready', prefs })
        setSavedEmail(prefs.email)
      },
      (e: unknown) => !ac.signal.aborted && setState({ status: 'error', message: e instanceof ApiError ? e.message : t('comp.notify.loadFailed') }),
    )
    return () => ac.abort()
  }, [userId, t])

  const prefs = state.status === 'ready' ? state.prefs : null
  const change = (patch: Partial<NotifyPrefs>) => setState((s) => (s.status === 'ready' ? { status: 'ready', prefs: { ...s.prefs, ...patch } } : s))
  const failed = (e: unknown, fallback: Key) => setError(e instanceof ApiError ? e.message : t(fallback))

  const save = async () => {
    if (!prefs) return
    setBusy(true)
    setError('')
    try {
      await saveNotifyPrefs(inputOf(prefs), userId)
      onClose()
    } catch (e) {
      failed(e, 'comp.notify.saveFailed')
    } finally {
      setBusy(false)
    }
  }

  const sendLetter = async () => {
    if (!prefs) return
    setBusy(true)
    setError('')
    setNote('')
    try {
      // the letter goes to the saved address: save what was typed first
      let p = prefs
      if (prefs.email.trim() !== savedEmail) {
        p = await saveNotifyPrefs(inputOf(prefs), userId)
        setSavedEmail(p.email)
        change({ email: p.email, verified: p.verified })
      }
      await sendNotifyVerification(userId)
      setCodeSent(true)
      setNote(t('comp.notify.sent', { email: p.email }))
    } catch (e) {
      failed(e, 'comp.notify.sendFailed')
    } finally {
      setBusy(false)
    }
  }

  const confirm = async () => {
    setBusy(true)
    setError('')
    try {
      const p = await confirmNotifyCode(code.trim(), userId)
      // the toggles keep what is being edited
      setState((s) => (s.status === 'ready' ? { status: 'ready', prefs: { ...s.prefs, verified: p.verified } } : s))
      setCodeSent(false)
      setCode('')
      setNote('')
    } catch (e) {
      failed(e, 'comp.notify.confirmFailed')
    } finally {
      setBusy(false)
    }
  }

  const email = prefs?.email.trim() ?? ''
  const confirmed = !!prefs && prefs.verified && email === savedEmail
  return (
    <Sheet
      title={name ? t('comp.notify.titleFor', { name }) : t('comp.notify.title')}
      role="dialog"
      size="wide"
      busy={busy}
      onCancel={onClose}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: onClose },
        { label: t('common.save'), kind: 'primary', disabled: !prefs, run: () => void save() },
      ]}
    >
      <div className={styles.form}>
        <p className={styles.hint}>{t('comp.notify.intro')}</p>
        {state.status === 'loading' && (
          <p className={styles.hint} role="status">
            {t('common.loading')}
          </p>
        )}
        {state.status === 'error' && (
          <p className={styles.error} role="alert">
            {state.message}
          </p>
        )}
        {prefs && (
          <>
            {!prefs.mailReady && <p className={styles.warning}>{t('comp.notify.noMail')}</p>}
            <section className={styles.section}>
              <TextField
                label={t('comp.notify.email')}
                type="email"
                autoComplete="email"
                inputMode="email"
                placeholder="name@example.com"
                value={prefs.email}
                onChange={(e) => change({ email: e.target.value })}
              />
              <p className={confirmed ? styles.ok : styles.hint} role="status">
                {email === '' ? '' : confirmed ? (
                  <>
                    <Icon name="checkCircle" size={16} /> {t('comp.notify.verified')}
                  </>
                ) : prefs.bounced && email === savedEmail ? (
                  t('comp.notify.bounced')
                ) : (
                  t('comp.notify.unverified')
                )}
              </p>
              {!confirmed && (
                <div className={styles.row}>
                  <Button size="small" variant="secondary" icon="send" disabled={busy || !prefs.mailReady || email === ''} onClick={() => void sendLetter()}>
                    {t('comp.notify.sendLetter')}
                  </Button>
                </div>
              )}
              {note && <p className={styles.hint}>{note}</p>}
              {codeSent && !confirmed && (
                <div className={styles.codeRow}>
                  <TextField
                    label={t('comp.notify.code')}
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    maxLength={6}
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                  />
                  <Button size="small" variant="primary" disabled={busy || code.trim().length !== 6} onClick={() => void confirm()}>
                    {t('comp.notify.confirm')}
                  </Button>
                </div>
              )}
            </section>
            <section className={styles.section} aria-labelledby={`${id}-topics`}>
              <h3 className={styles.label} id={`${id}-topics`}>
                {t('comp.notify.topics')}
              </h3>
              <ul className={styles.list}>
                {prefs.topics.map((topic) => {
                  const l = topicLabel(topic)
                  if (!l) return null
                  const hintId = l.hint ? `${id}-${topic}-hint` : undefined
                  return (
                    <li key={topic} className={styles.item}>
                      <div className={styles.itemText}>
                        <span aria-hidden>{t(l.label)}</span>
                        {l.hint && (
                          <span className={styles.hint} id={hintId}>
                            {t(l.hint)}
                          </span>
                        )}
                      </div>
                      <Toggle label={t(l.label)} checked={prefs[topic]} describedBy={hintId} onChange={(on) => change({ [topic]: on })} />
                    </li>
                  )
                })}
                <li className={styles.item}>
                  <div className={styles.itemText}>
                    <span aria-hidden>{t('comp.notify.quiet')}</span>
                    <span className={styles.hint} id={`${id}-quiet-hint`}>
                      {t('comp.notify.quietHint')}
                    </span>
                  </div>
                  <Toggle label={t('comp.notify.quiet')} checked={prefs.quietHours} describedBy={`${id}-quiet-hint`} onChange={(quietHours) => change({ quietHours })} />
                </li>
              </ul>
              <p className={styles.hint}>{t('comp.notify.digest')}</p>
              {!prefs.publicLinks && <p className={styles.hint}>{t('comp.notify.noLinks')}</p>}
            </section>
          </>
        )}
        {error && (
          <p className={styles.error} role="alert">
            {error}
          </p>
        )}
      </div>
    </Sheet>
  )
}
