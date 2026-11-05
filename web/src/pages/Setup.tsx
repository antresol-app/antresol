// First run: «Добро пожаловать в Vault». Creates the administrator profile and
// signs in straight away.
import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { motion } from 'motion/react'
import { ApiError } from '../api/client'
import { Button } from '../ui/Button'
import { LogoMark } from '../components/Logo'
import { SourceLink } from '../components/SourceLink'
import { TextField } from '../ui/TextField'
import { session, useSession } from '../state/session'
import { useReducedMotion } from '../state/useMediaQuery'
import styles from './Auth.module.css'
import { useT } from '../i18n'

export function Setup() {
  const navigate = useNavigate()
  const t = useT()
  const reduce = useReducedMotion()
  const { remote } = useSession()
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [errors, setErrors] = useState<{ name?: string; password?: string; form?: string }>({})
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const next: typeof errors = {}
    if (!name.trim()) next.name = t('app.setup.nameRequired')
    if (!password) next.password = t('app.setup.passwordRequired')
    else if ([...password].length < 6) next.password = t('app.setup.passwordShort')
    setErrors(next)
    if (next.name || next.password) return
    setBusy(true)
    try {
      await session.setup(name.trim(), password)
      navigate('/', { replace: true })
    } catch (err) {
      setErrors({ form: err instanceof ApiError ? err.message : t('app.failedTryAgain') })
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className={styles.screen}>
      <motion.section
        className={`${styles.stack} ${styles.welcome}`}
        initial={{ opacity: 0, y: reduce ? 0 : 16 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: reduce ? 0 : 0.6, ease: [0.22, 1, 0.36, 1] }}
      >
        <div className={styles.welcomeMark}>
          <LogoMark size={76} />
        </div>
        <h1 className={styles.welcomeTitle}>{t('app.setup.welcome')}</h1>
        <p className={styles.lead}>{t('app.setup.lead')}</p>

        {remote ? (
          <div className={`${styles.card} glass`} role="alert">
            <p className={styles.lead} style={{ margin: 0 }}>
              {t('app.setup.remote')}
            </p>
          </div>
        ) : (
          <form className={`${styles.card} glass`} onSubmit={(e) => void submit(e)} noValidate>
            <TextField
              label={t('app.setup.name')}
              autoComplete="name"
              autoFocus
              placeholder={t('app.setup.namePlaceholder')}
              value={name}
              onChange={(e) => setName(e.target.value)}
              error={errors.name}
            />
            <TextField
              label={t('common.password')}
              type="password"
              autoComplete="new-password"
              placeholder={t('app.setup.passwordPlaceholder')}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              error={errors.password}
              hint={t('app.setup.passwordHint')}
              revealable
            />
            {errors.form && (
              <p className={styles.inlineError} style={{ margin: 0 }} role="alert">
                {errors.form}
              </p>
            )}
            <Button type="submit" size="large" loading={busy}>
              {t('app.setup.continue')}
            </Button>
          </form>
        )}
        <p className={styles.footnote}>{t('app.setup.footnote')}</p>
        <SourceLink />
      </motion.section>
    </main>
  )
}
