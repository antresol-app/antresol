// «Кто смотрит?» — the Apple TV profile picker. A profile without a password
// signs in with one click (at home only: from outside it explains why not);
// a protected one asks for its password in place.
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { AnimatePresence, motion } from 'motion/react'
import { ApiError, getProfiles } from '../api/client'
import type { Profile } from '../api/types'
import { Avatar } from '../components/Avatar'
import { Button } from '../ui/Button'
import { Icon } from '../components/Icon'
import { LogoMark } from '../components/Logo'
import { SourceLink } from '../components/SourceLink'
import { TextField } from '../ui/TextField'
import { remotePasswordless, session, useSession } from '../state/session'
import { SHORT_QUERY, useIsCompact, useMediaQuery, useReducedMotion } from '../state/useMediaQuery'
import styles from './Auth.module.css'
import { useT } from '../i18n'

type Load = { state: 'loading' } | { state: 'error'; message: string } | { state: 'ready'; profiles: Profile[] }

function destination(state: unknown): string {
  const from = (state as { from?: string } | null)?.from
  return from && from.startsWith('/') && !from.startsWith('/profiles') && !from.startsWith('/welcome') ? from : '/'
}

export function ProfilePicker() {
  const navigate = useNavigate()
  const t = useT()
  const location = useLocation()
  const compact = useIsCompact()
  const reduce = useReducedMotion()
  const { remote } = useSession()
  const [load, setLoad] = useState<Load>({ state: 'loading' })
  const [selected, setSelected] = useState<Profile | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [shake, setShake] = useState(0)
  const pwRef = useRef<HTMLInputElement>(null)

  const fetchProfiles = () => {
    setLoad({ state: 'loading' })
    getProfiles()
      .then((profiles) => setLoad({ state: 'ready', profiles }))
      .catch((e: unknown) =>
        setLoad({ state: 'error', message: e instanceof ApiError ? e.message : t('app.profiles.loadFailed') }),
      )
  }
  useEffect(fetchProfiles, [])

  const done = () => navigate(destination(location.state), { replace: true })

  const choose = async (p: Profile) => {
    setError(null)
    if (p.hasPassword) {
      setPassword('')
      setSelected(p)
      return
    }
    if (remote) {
      setError(remotePasswordless())
      return
    }
    setBusyId(p.id)
    try {
      await session.login(p.id)
      done()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t('app.profiles.signInFailed'))
    } finally {
      setBusyId(null)
    }
  }

  const submitPassword = async (e: FormEvent) => {
    e.preventDefault()
    if (!selected) return
    if (!password) {
      setError(t('app.profiles.enterPassword'))
      return
    }
    setBusyId(selected.id)
    setError(null)
    try {
      await session.login(selected.id, password)
      done()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t('app.profiles.signInFailed'))
      setShake((n) => n + 1)
      setPassword('')
      pwRef.current?.focus()
    } finally {
      setBusyId(null)
    }
  }

  // a phone on its side: everything must fit above the keyboard
  const short = useMediaQuery(SHORT_QUERY)
  const size = short ? 72 : compact ? 96 : 132
  const fade = { duration: reduce ? 0 : 0.35, ease: [0.22, 1, 0.36, 1] as const }

  return (
    <main className={styles.screen}>
      <div className={styles.brand}>
        <LogoMark size={36} />
      </div>
      <AnimatePresence mode="wait" initial={false}>
        {!selected ? (
          <motion.section
            key="pick"
            className={styles.stack}
            initial={{ opacity: 0, scale: reduce ? 1 : 0.98 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0, scale: reduce ? 1 : 1.02 }}
            transition={fade}
          >
            <h1 className={styles.title}>{t('app.profiles.whoIsWatching')}</h1>

            {load.state === 'loading' && (
              <div className={styles.profiles} aria-busy="true" aria-label={t('app.profiles.loading')}>
                {[0, 1, 2].map((i) => (
                  <div key={i} className={styles.profileSkeleton} style={{ width: size, height: size }} />
                ))}
              </div>
            )}

            {load.state === 'error' && (
              <div className={styles.problem} role="alert">
                <p>{load.message}</p>
                <Button variant="secondary" onClick={fetchProfiles}>
                  {t('common.retry')}
                </Button>
              </div>
            )}

            {load.state === 'ready' && (
              <ul className={styles.profiles}>
                {load.profiles.map((p, i) => (
                  <motion.li
                    key={p.id}
                    initial={{ opacity: 0, y: reduce ? 0 : 10 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{ ...fade, delay: reduce ? 0 : 0.05 * i }}
                  >
                    <button
                      type="button"
                      className={styles.profile}
                      onClick={() => void choose(p)}
                      disabled={busyId !== null}
                      aria-busy={busyId === p.id || undefined}
                    >
                      <span className={styles.avatarWrap}>
                        <Avatar name={p.name} avatar={p.avatar} size={size} />
                        {p.hasPassword && (
                          <span className={`${styles.lock} glass`} aria-hidden>
                            <Icon name="lock" size={compact ? 17 : 21} strokeWidth={2.3} />
                          </span>
                        )}
                        {busyId === p.id && <span className={styles.avatarBusy} aria-hidden />}
                      </span>
                      <span className={styles.profileName}>
                        {p.name}
                        {p.hasPassword && <span className="visually-hidden">{t('app.profiles.withPassword')}</span>}
                      </span>
                    </button>
                  </motion.li>
                ))}
              </ul>
            )}
            {error && !selected && (
              <p className={styles.inlineError} role="alert">
                {error}
              </p>
            )}
            <SourceLink />
          </motion.section>
        ) : (
          <motion.section
            key="password"
            className={styles.stack}
            initial={{ opacity: 0, scale: reduce ? 1 : 0.98 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0 }}
            transition={fade}
          >
            <Avatar name={selected.name} avatar={selected.avatar} size={short ? 64 : compact ? 104 : 120} />
            <h1 className={styles.subtitleTitle}>{selected.name}</h1>
            <p className={styles.lead}>{t('app.profiles.enterPasswordToContinue')}</p>
            <motion.form
              key={shake}
              className={styles.passwordForm}
              onSubmit={(e) => void submitPassword(e)}
              onKeyDown={(e) => {
                if (e.key === 'Escape') {
                  setSelected(null)
                  setError(null)
                }
              }}
              animate={shake && !reduce ? { x: [0, -10, 9, -6, 4, 0] } : undefined}
              transition={{ duration: 0.42 }}
              noValidate
            >
              <TextField
                ref={pwRef}
                label={t('common.password')}
                hideLabel
                type="password"
                placeholder={t('common.password')}
                autoComplete="current-password"
                autoFocus
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                error={error}
                revealable
              />
              <div className={styles.row}>
                <Button
                  variant="secondary"
                  size="large"
                  onClick={() => {
                    setSelected(null)
                    setError(null)
                  }}
                >
                  {t('common.back')}
                </Button>
                <Button type="submit" size="large" loading={busyId === selected.id}>
                  {t('app.profiles.signIn')}
                </Button>
              </div>
            </motion.form>
          </motion.section>
        )}
      </AnimatePresence>
    </main>
  )
}
