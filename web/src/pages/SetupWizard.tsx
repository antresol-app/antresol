// The first-run wizard: what comes after the administrator is created. Media
// folders (what the server sees, which folder is which kind), write access (a
// real test, with the fix to copy when it fails), the graphics card, the
// external services (every one checked; «Готово» saves the choice), then
// «Готово» starts the library scan. Every step can be skipped; the same
// checks stay in «Управление → Настройки сервера». Only reachable while the
// server says the wizard is pending (see GET /api/setup/wizard), and only for
// an administrator.
import { useEffect, useState } from 'react'
import { Navigate, useNavigate } from 'react-router'
import { ApiError, errorText, getTranscoding, probeTranscoding } from '../api/client'
import { finishSetupWizard, getSetupWizard } from '../api/storage'
import { EXTERNAL_SERVICES, getExternal, saveExternal, type ExternalServiceId, type ExternalView } from '../api/external'
import { ExternalServiceRows } from './manage/ExternalServicesSettings'
import type { TranscodingView } from '../api/types'
import { GpuHint } from '../components/storage/GpuHint'
import { RemoveConfirm, StorageFolders } from '../components/storage/StorageFolders'
import { StoragePerms } from '../components/storage/StoragePerms'
import { useStorage } from '../components/storage/useStorage'
import { LogoMark } from '../components/Logo'
import { useT } from '../i18n'
import { Button } from '../ui/Button'
import { GroupedSection, ListRow } from '../ui/List'
import { Notice } from '../ui/Notice'
import { statusLine } from './manage/TranscodingSettings'
import styles from './SetupWizard.module.css'

const STEPS = 4

/** Every service checked: the wizard offers them all (a locked one shows the server's word). */
const ALL_ON = Object.fromEntries(EXTERNAL_SERVICES.map((id) => [id, true])) as Record<ExternalServiceId, boolean>

/** The graphics card: the status line the settings and «О сервере» show, and the fix when none was passed. */
function HardwareStep() {
  const t = useT()
  const [view, setView] = useState<TranscodingView | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    const ac = new AbortController()
    getTranscoding(ac.signal).then(setView, (e: unknown) => !ac.signal.aborted && setError(errorText(e, t('settings.tc.loadFailed'))))
    return () => ac.abort()
  }, [t])
  const probe = async () => {
    setBusy(true)
    try {
      setView(await probeTranscoding())
      setError('')
    } catch (e) {
      setError(errorText(e, t('settings.tc.saveFailed')))
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <GroupedSection>
        <ListRow label={t('settings.tc.status')} status={error || (busy ? t('settings.tc.probing') : view ? statusLine(view, t) : t('common.loading'))}>
          <Button size="small" variant="secondary" icon="restart" loading={busy} disabled={busy || !view} onClick={() => void probe()}>
            {t('settings.tc.probe')}
          </Button>
        </ListRow>
      </GroupedSection>
      {view && <GpuHint view={view} />}
    </>
  )
}

/** The external services: a switch each, all on; the choice is saved by «Готово». */
function ExternalStep({ view, chosen, onChange }: { view: ExternalView | null; chosen: Record<ExternalServiceId, boolean>; onChange: (id: ExternalServiceId, on: boolean) => void }) {
  const rows = EXTERNAL_SERVICES.map((id) => {
    const s = view?.services.find((x) => x.id === id)
    return s?.locked ? { id, on: s.on, locked: true } : { id, on: chosen[id] }
  })
  return (
    <GroupedSection>
      <ExternalServiceRows rows={rows} onChange={onChange} />
    </GroupedSection>
  )
}

export function SetupWizard() {
  const t = useT()
  const navigate = useNavigate()
  const storage = useStorage()
  const [pending, setPending] = useState<boolean | null>(null)
  const [step, setStep] = useState(0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [asking, setAsking] = useState(false)
  const [ext, setExt] = useState(ALL_ON)
  const [extView, setExtView] = useState<ExternalView | null>(null)
  useEffect(() => {
    if (step !== 3) return
    const ac = new AbortController()
    // which services the server's environment locks (shown, not sent)
    getExternal(ac.signal).then(setExtView, () => {})
    return () => ac.abort()
  }, [step])

  useEffect(() => {
    const ac = new AbortController()
    // not an administrator, or not signed in: nothing to show here either
    getSetupWizard(ac.signal).then(
      (r) => setPending(r.pending),
      () => !ac.signal.aborted && setPending(false),
    )
    return () => ac.abort()
  }, [])

  if (pending === null) {
    return (
      <main className={styles.screen}>
        <div className={styles.busy} role="status" aria-label={t('common.loading')} />
      </main>
    )
  }
  if (!pending) return <Navigate to="/" replace />

  const restarting = storage.restarting
  const finish = async () => {
    setBusy(true)
    try {
      await finishSetupWizard()
      navigate('/', { replace: true })
    } catch (e) {
      // refused: another tab has ended the wizard already, so it is over
      if (e instanceof ApiError && e.status === 403) {
        navigate('/', { replace: true })
        return
      }
      setError(errorText(e, t('wizard.failed')))
      setBusy(false)
    }
  }
  const next = async (confirm = false) => {
    if (step === 0 && storage.dirty) {
      // titles would leave the library: ask first
      if (!confirm && storage.removals.length > 0) {
        setAsking(true)
        return
      }
      setAsking(false)
      if (!(await storage.save(false, confirm))) return
    }
    if (step === 3) {
      setBusy(true)
      try {
        const locked = new Set(extView?.services.filter((s) => s.locked).map((s) => s.id))
        await saveExternal({ services: Object.fromEntries(EXTERNAL_SERVICES.filter((id) => !locked.has(id)).map((id) => [id, ext[id]])) })
      } catch (e) {
        setError(errorText(e, t('wizard.failed')))
        setBusy(false)
        return
      }
    }
    if (step === STEPS - 1) await finish()
    else setStep(step + 1)
  }
  const skip = async () => {
    // a folder picked and skipped stays unsaved: show the saved ones again
    if (step === 0 && storage.dirty) await storage.discard()
    if (step === STEPS - 1) await finish()
    else setStep(step + 1)
  }

  const titles = ['wizard.folders.title', 'wizard.perms.title', 'wizard.hw.title', 'wizard.ext.title'] as const
  const leads = ['wizard.folders.lead', 'wizard.perms.lead', 'wizard.hw.lead', 'wizard.ext.lead'] as const
  const root = storage.view?.root.path ?? '/media'
  const disabled = busy || storage.busy || restarting
  return (
    <main className={styles.screen}>
      <div className={`${styles.column} enter-rise`}>
        <header className={styles.header}>
          <div className={styles.mark}>
            <LogoMark size={48} />
          </div>
          <p className={styles.step}>{t('wizard.step', { n: step + 1, total: STEPS })}</p>
          <h1 className={styles.title}>{t(titles[step]!)}</h1>
          {step === 0 && <p className={styles.lead}>{t('wizard.lead')}</p>}
          <p className={styles.lead}>{t(leads[step]!, { root })}</p>
        </header>

        <div className={styles.body}>
          {restarting && <Notice tone="accent">{t('wizard.restarting')}</Notice>}
          {storage.slow && <Notice tone="warning">{t('wizard.restartSlow')}</Notice>}
          {storage.error && (
            <p className={styles.error} role="alert">
              {storage.error}
            </p>
          )}
          {step === 0 && <StorageFolders storage={storage} />}
          {(asking || storage.confirmNeeded) && (
            <RemoveConfirm
              removals={storage.removals}
              busy={storage.busy}
              onCancel={() => {
                setAsking(false)
                storage.dismissConfirm()
              }}
              onConfirm={() => void next(true)}
            />
          )}
          {step === 1 && storage.view && (
            <>
              <StoragePerms view={storage.view} />
              <div className={styles.actions}>
                <Button size="small" variant="secondary" icon="restart" loading={storage.busy && !restarting} disabled={disabled} onClick={() => void storage.recheck()}>
                  {t('storage.check')}
                </Button>
              </div>
            </>
          )}
          {step === 2 && <HardwareStep />}
          {step === 3 && <ExternalStep view={extView} chosen={ext} onChange={(id, on) => setExt({ ...ext, [id]: on })} />}
        </div>

        {error && (
          <p className={styles.error} role="alert">
            {error}
          </p>
        )}
        {step === STEPS - 1 && <p className={styles.hint}>{t('wizard.finishHint')}</p>}
        <div className={styles.actions}>
          {step > 0 && (
            <Button variant="secondary" disabled={disabled} onClick={() => setStep(step - 1)}>
              {t('wizard.back')}
            </Button>
          )}
          <span className={styles.spacer} />
          <Button variant="secondary" disabled={disabled} onClick={() => void skip()}>
            {t('wizard.skip')}
          </Button>
          <Button variant="primary" loading={busy || restarting} disabled={disabled || (step === 0 && !storage.view)} onClick={() => void next()}>
            {step === STEPS - 1 ? t('wizard.finish') : t('wizard.next')}
          </Button>
        </div>
        <Button variant="link" className={styles.skipAll} disabled={disabled} onClick={() => void finish()}>
          {t('wizard.skipAll')}
        </Button>
      </div>
    </main>
  )
}
