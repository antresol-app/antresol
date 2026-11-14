// «Пароль для приложений»: the one password music apps (Subsonic: Amperfy,
// play:Sub) and book readers (OPDS: KOReader) sign in with. Shows what to
// type in the app: the server address, the profile name and the password.
// The password is shown once, right after it is created; afterwards only
// «Создать новый» (which replaces it) or «Отозвать».
import { Button } from '../ui/Button'
import { useCallback, useEffect, useRef, useState } from 'react'
import { createAppPassword, getAppPassword, revokeAppPassword, errorText } from '../api/client'
import { locale, t as tr, useT } from '../i18n'
import { Sheet } from '../ui/Sheet'
import styles from './AppPasswordSheet.module.css'

export interface AppPasswordSheetProps {
  userName: string
  /** The profile has no password: the server lets its apps in only from home. */
  passwordless?: boolean
  onClose: () => void
}

type State =
  | { status: 'loading' }
  | { status: 'error'; message: string }
  /** `shown`: just created, visible this once. */
  | { status: 'ready'; exists: boolean; createdAt: string | null; shown: string | null }

type Confirm = 'revoke' | 'replace' | null

const errText = (e: unknown) => (errorText(e, tr('comp.serverUnreachable')))

/** «25 октября 2026». */
const createdLabel = (iso: string | null) => {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : tr('comp.appPassword.createdOn', { date: d.toLocaleDateString(locale(), { day: 'numeric', month: 'long', year: 'numeric' }) })
}

async function copyText(text: string, input?: HTMLInputElement | null): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    // no clipboard API (plain http): select and copy the old way
    if (!input) return false
    input.select()
    try {
      return document.execCommand('copy')
    } catch {
      return false
    }
  }
}

function CopyRow({ label, value, field }: { label: string; value: string; field?: boolean }) {
  const t = useT()
  const [copied, setCopied] = useState(false)
  const ref = useRef<HTMLInputElement>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => void (timer.current && clearTimeout(timer.current)), [])
  return (
    <div className={styles.row}>
      <span className={styles.label}>{label}</span>
      <input
        ref={ref}
        className={`${styles.value} ${field ? styles.secret : ''}`}
        readOnly
        value={value}
        aria-label={label}
        onFocus={(e) => e.currentTarget.select()}
      />
      <Button
        variant="secondary"
        size="small"
        icon={copied ? 'check' : undefined}
        className={styles.copy}
        aria-label={t('comp.appPassword.copyLabel', { label: label.toLowerCase() })}
        onClick={() =>
          void copyText(value, ref.current).then((ok) => {
            if (!ok) return
            setCopied(true)
            if (timer.current) clearTimeout(timer.current)
            timer.current = setTimeout(() => setCopied(false), 2000)
          })
        }
      >
        {copied ? t('comp.appPassword.copied') : t('comp.appPassword.copy')}
      </Button>
    </div>
  )
}

export function AppPasswordSheet({ userName, passwordless, onClose }: AppPasswordSheetProps) {
  const t = useT()
  const [state, setState] = useState<State>({ status: 'loading' })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [confirm, setConfirm] = useState<Confirm>(null)
  const origin = window.location.origin

  const load = useCallback(() => {
    let live = true
    setState({ status: 'loading' })
    getAppPassword()
      // the server never sends the stored password: only whether there is one
      .then((r) => live && setState({ status: 'ready', exists: r.exists, createdAt: r.createdAt, shown: null }))
      .catch((e: unknown) => live && setState({ status: 'error', message: errText(e) }))
    return () => {
      live = false
    }
  }, [])
  useEffect(() => load(), [load])

  const create = () => {
    setConfirm(null)
    setBusy(true)
    setError('')
    createAppPassword()
      .then((r) => setState({ status: 'ready', exists: true, createdAt: new Date().toISOString(), shown: r.password }))
      .catch((e: unknown) => setError(errText(e)))
      .finally(() => setBusy(false))
  }
  const revoke = () => {
    setConfirm(null)
    setBusy(true)
    setError('')
    revokeAppPassword()
      .then(() => setState({ status: 'ready', exists: false, createdAt: null, shown: null }))
      .catch((e: unknown) => setError(errText(e)))
      .finally(() => setBusy(false))
  }

  const ready = state.status === 'ready' ? state : null

  return (
    <>
      <Sheet
        title={t('comp.appPassword.title')}
        role="dialog"
        size="wide"
        message={
          <>{t('comp.appPassword.message', { origin, name: userName })}</>
        }
        onCancel={onClose}
        closeButton={false}
        actions={[{ label: t('common.done'), kind: 'primary', run: onClose }]}
      >
        {/* focus lands here, not in the server field (no stray selection) */}
        <div className={styles.body} tabIndex={-1} data-autofocus>
          <CopyRow label={t('comp.appPassword.server')} value={origin} />
          <CopyRow label={t('common.name')} value={userName} />
          {passwordless && (
            <p className={styles.note} data-testid="app-password-home-only">
              {t('comp.appPassword.homeOnly')}
            </p>
          )}

          {state.status === 'loading' && <p className={styles.note}>{t('common.loadingEllipsis')}</p>}
          {state.status === 'error' && (
            <div className={styles.retry} role="alert">
              <span className={styles.error}>{state.message}</span>
              <Button variant="secondary" size="small" onClick={load}>
                {t('common.retry')}
              </Button>
            </div>
          )}

          {ready?.shown && (
            <>
              <CopyRow label={t('common.password')} value={ready.shown} field />
              <p className={styles.note}>{t('comp.appPassword.saveIt')}</p>
            </>
          )}
          {ready && !ready.shown && ready.exists && (
            <div className={styles.row}>
              <span className={styles.label}>{t('common.password')}</span>
              <span className={`${styles.value} ${styles.hidden}`} aria-hidden>
                ••••••••••••
              </span>
              <span className={styles.created}>{createdLabel(ready.createdAt) || t('comp.appPassword.created')}</span>
            </div>
          )}
          {ready && !ready.shown && ready.exists && (
            <p className={styles.note}>{t('comp.appPassword.shownOnce')}</p>
          )}
          {ready && !ready.exists && <p className={styles.note}>{t('comp.appPassword.none')}</p>}

          {ready && (
            <div className={styles.buttons}>
              {ready.exists ? (
                <>
                  <Button variant="secondary" size="small" className={styles.button} disabled={busy} onClick={() => setConfirm('replace')}>
                    {t('comp.appPassword.createNew')}
                  </Button>
                  <Button variant="destructive" size="small" className={styles.button} disabled={busy} onClick={() => setConfirm('revoke')}>
                    {t('comp.appPassword.revoke')}
                  </Button>
                </>
              ) : (
                <Button size="small" className={styles.button} disabled={busy} onClick={create}>
                  {t('comp.appPassword.create')}
                </Button>
              )}
            </div>
          )}
          {error && (
            <p className={styles.error} role="alert">
              {error}
            </p>
          )}

          <p className={styles.opds}>
            {t('comp.appPassword.opds')}<span className={styles.url}>{origin}/opds</span>
          </p>
        </div>
      </Sheet>

      {confirm === 'revoke' && (
        <Sheet
          title={t('comp.appPassword.revokeTitle')}
          message={t('comp.appPassword.revokeMessage')}
          onCancel={() => setConfirm(null)}
          actions={[
            { label: t('common.cancelAction'), kind: 'cancel', run: () => setConfirm(null) },
            { label: t('comp.appPassword.revoke'), kind: 'destructive', run: revoke },
          ]}
        />
      )}
      {confirm === 'replace' && (
        <Sheet
          title={t('comp.appPassword.replaceTitle')}
          message={t('comp.appPassword.replaceMessage')}
          onCancel={() => setConfirm(null)}
          actions={[
            { label: t('common.cancelAction'), kind: 'cancel', run: () => setConfirm(null) },
            { label: t('common.create'), kind: 'primary', run: create },
          ]}
        />
      )}
    </>
  )
}
