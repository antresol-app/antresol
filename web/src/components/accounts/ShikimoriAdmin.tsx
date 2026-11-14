// «Настройки сервера → Shikimori»: the OAuth client that lets profiles mark
// on Shikimori too. The secret is written, never shown again; the redirect
// URI is what to enter in Shikimori's application. Nothing here when the
// server has no accounts.
import { useEffect, useRef, useState } from 'react'
import { errorText, getShikimoriAdmin, putShikimoriAdmin } from '../../api/client'
import type { ShikimoriAdminSettings } from '../../api/types'
import { useT } from '../../i18n'
import { GroupedSection, ListRow } from '../../ui/List'
import { TextField } from '../../ui/TextField'
import styles from '../../pages/manage/Settings.module.css'

export function ShikimoriAdmin() {
  const t = useT()
  const [cfg, setCfg] = useState<ShikimoriAdminSettings | null>(null)
  const [id, setId] = useState('')
  const [secret, setSecret] = useState('')
  const [saved, setSaved] = useState<'' | 'saved' | 'failed'>('')
  const [error, setError] = useState('')
  const stored = useRef('')

  useEffect(() => {
    const ac = new AbortController()
    getShikimoriAdmin(ac.signal).then(
      (c) => {
        setCfg(c)
        setId(c.clientId)
        stored.current = c.clientId
      },
      () => undefined, // not mounted here, or not an admin
    )
    return () => ac.abort()
  }, [])

  if (!cfg) return null
  const commit = async () => {
    if (cfg.fromEnv || (id === stored.current && !secret)) return
    try {
      await putShikimoriAdmin(id.trim(), secret)
      stored.current = id.trim()
      setCfg({ ...cfg, clientId: id.trim(), hasSecret: cfg.hasSecret || secret !== '' })
      setSecret('')
      setError('')
      setSaved('saved')
    } catch (e) {
      setSaved('failed')
      setError(errorText(e, t('acct.failed')))
    }
  }

  return (
    <GroupedSection title="Shikimori" footer={cfg.fromEnv ? t('acct.admin.fromEnv') : undefined}>
      <ListRow wide label={t('acct.admin.client')} hint={t('acct.admin.hint')} status={saved === 'saved' ? t('acct.admin.saved') : error || undefined}>
        <div className={styles.aiFields}>
          <TextField label="Client ID" value={id} readOnly={cfg.fromEnv} onChange={(e) => setId(e.target.value)} onBlur={() => void commit()} autoComplete="off" spellCheck={false} />
          <TextField
            label="Client Secret"
            type="password"
            value={secret}
            readOnly={cfg.fromEnv}
            placeholder={cfg.hasSecret ? t('acct.admin.secretSaved') : ''}
            onChange={(e) => setSecret(e.target.value)}
            onBlur={() => void commit()}
            autoComplete="new-password"
          />
        </div>
      </ListRow>
      <ListRow wide label={t('acct.admin.redirect')} hint={t('acct.admin.redirect.hint')}>
        <div className={styles.aiFields}>
          <TextField label="Redirect URI" value={cfg.redirectUri || t('acct.admin.noUrl')} readOnly onFocus={(e) => e.currentTarget.select()} />
          <TextField label="Scope" value={cfg.scope} readOnly />
        </div>
      </ListRow>
    </GroupedSection>
  )
}
