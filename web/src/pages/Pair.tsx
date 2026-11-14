// «Вход на телевизоре» (/pair?code=…): the page a phone's camera opens from
// the QR code a TV shows (the TV's own side is in the apps). Behind the
// sign-in gate like every page, so a signed-out phone signs in first and
// comes back here. It shows the device that asks, and the profile it will
// open: the caller's own, or for an admin any profile; then «Подтвердить
// вход» or «Отклонить». A code works once and for five minutes.
import { useEffect, useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router'
import { errorText, getProfiles } from '../api/client'
import { approvePairing, denyPairing, lookupPairing, type PairingRequest } from '../api/pairing'
import type { Profile } from '../api/types'
import { APP_NAME } from '../brand'
import { useT } from '../i18n'
import { useSession } from '../state/session'
import { Button } from '../ui/Button'
import { GroupedSection, ListRow } from '../ui/List'
import { Notice } from '../ui/Notice'
import { Select } from '../ui/Select'
import { TextField } from '../ui/TextField'
import m from './manage/Manage.module.css'
import styles from './Pair.module.css'

type State =
  | { kind: 'enter'; error?: string }
  | { kind: 'loading' }
  | { kind: 'found'; req: PairingRequest; error?: string }
  | { kind: 'approved'; name: string }
  | { kind: 'denied' }

/** The code as typed or scanned: letters and digits, upper case. */
const clean = (s: string) => s.toUpperCase().replace(/[^A-Z0-9]/g, '')

export function Pair() {
  const t = useT()
  const { user } = useSession()
  const [params, setParams] = useSearchParams()
  const fromUrl = clean(params.get('code') ?? '')
  const [typed, setTyped] = useState(fromUrl)
  const [state, setState] = useState<State>(fromUrl ? { kind: 'loading' } : { kind: 'enter' })
  const [profiles, setProfiles] = useState<Profile[]>([])
  const [target, setTarget] = useState(String(user?.id ?? ''))
  const [busy, setBusy] = useState<'' | 'approve' | 'deny'>('')

  useEffect(() => {
    document.title = `${t('pair.title')} — ${APP_NAME}`
  }, [t])

  // an admin may sign the TV in to any profile (a kid's TV)
  useEffect(() => {
    if (!user?.isAdmin) return
    getProfiles()
      .then(setProfiles)
      .catch(() => setProfiles([]))
  }, [user?.isAdmin])

  useEffect(() => {
    if (!fromUrl) return
    let live = true
    setState({ kind: 'loading' })
    lookupPairing(fromUrl)
      .then((req) => live && setState({ kind: 'found', req }))
      .catch((e) => live && setState({ kind: 'enter', error: errorText(e, t('pair.failed')) }))
    return () => {
      live = false
    }
  }, [fromUrl, t])

  if (!user) return null

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const code = clean(typed)
    if (code) setParams({ code }, { replace: true })
  }

  const approve = (req: PairingRequest) => {
    const id = Number(target) || user.id
    setBusy('approve')
    approvePairing(req.code, id === user.id ? undefined : id)
      .then(() => setState({ kind: 'approved', name: profiles.find((p) => p.id === id)?.name ?? user.name }))
      .catch((e) => setState({ kind: 'found', req, error: errorText(e, t('pair.failed')) }))
      .finally(() => setBusy(''))
  }

  const deny = (req: PairingRequest) => {
    setBusy('deny')
    denyPairing(req.code)
      .then(() => setState({ kind: 'denied' }))
      .catch((e) => setState({ kind: 'found', req, error: errorText(e, t('pair.failed')) }))
      .finally(() => setBusy(''))
  }

  const another = () => {
    setTyped('')
    setParams({}, { replace: true })
    setState({ kind: 'enter' })
  }

  return (
    <div className={m.page}>
      <section aria-labelledby="pair-title" className={styles.column}>
        <header className={m.header}>
          <div>
            <h1 id="pair-title" className={m.title}>
              {t('pair.title')}
            </h1>
            <p className={m.subtitle}>{t('pair.lead')}</p>
          </div>
        </header>

        {state.kind === 'enter' && (
          <form className={styles.form} onSubmit={submit}>
            {state.error && (
              <Notice tone="destructive" className={styles.notice}>
                {state.error}
              </Notice>
            )}
            <TextField
              label={t('pair.code')}
              hint={t('pair.codeHint')}
              value={typed}
              onChange={(e) => setTyped(e.target.value.toUpperCase())}
              autoComplete="one-time-code"
              autoCapitalize="characters"
              spellCheck={false}
              maxLength={9}
              autoFocus
            />
            <div className={styles.actions}>
              <Button type="submit" disabled={clean(typed).length !== 6}>
                {t('pair.continue')}
              </Button>
            </div>
          </form>
        )}

        {state.kind === 'loading' && <p className={m.subtitle} aria-busy="true">{t('common.loading')}</p>}

        {state.kind === 'found' && (
          <>
            {state.req.remote && (
              <Notice className={styles.notice} title={t('pair.remoteTitle')}>
                {t('pair.remote')}
              </Notice>
            )}
            {state.error && (
              <Notice tone="destructive" className={styles.notice}>
                {state.error}
              </Notice>
            )}
            <GroupedSection>
              <ListRow label={t('pair.device')}>{state.req.device.name || t('pair.unnamed')}</ListRow>
              {state.req.device.model && <ListRow label={t('pair.model')}>{state.req.device.model}</ListRow>}
              <ListRow label={t('pair.profile')}>
                {user.isAdmin && profiles.length > 1 ? (
                  <Select
                    label={t('pair.profile')}
                    placement="bottom-end"
                    value={target}
                    options={profiles.map((p) => ({ value: String(p.id), label: p.name }))}
                    onChange={(v: string) => setTarget(v)}
                  />
                ) : (
                  user.name
                )}
              </ListRow>
            </GroupedSection>
            <div className={styles.actions}>
              <Button loading={busy === 'approve'} disabled={busy !== ''} onClick={() => approve(state.req)}>
                {t('pair.approve')}
              </Button>
              <Button variant="secondary" loading={busy === 'deny'} disabled={busy !== ''} onClick={() => deny(state.req)}>
                {t('pair.deny')}
              </Button>
            </div>
          </>
        )}

        {state.kind === 'approved' && (
          <Notice tone="accent" icon="checkCircle" className={styles.notice} title={t('pair.approvedTitle')}>
            {t('pair.approved', { name: state.name })}
          </Notice>
        )}

        {state.kind === 'denied' && (
          <Notice tone="accent" className={styles.notice} title={t('pair.deniedTitle')}>
            {t('pair.denied')}
          </Notice>
        )}

        {(state.kind === 'approved' || state.kind === 'denied') && (
          <div className={styles.actions}>
            <Button variant="secondary" onClick={another}>
              {t('pair.another')}
            </Button>
          </div>
        )}
      </section>
    </div>
  )
}

export default Pair
