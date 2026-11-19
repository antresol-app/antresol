// «Внешние сервисы» (Настройки сервера): one switch per public service the
// server asks about the titles already in its library, with what it sends
// and what it gives, and a link to it. A switch applies at once (the server
// re-checks it before every request: no restart). A service the server's
// environment sets (its VAULT_* variable) shows locked. The first-run wizard
// lists the same rows (ExternalServiceRows) with every service checked.
import { useEffect, useState } from 'react'
import { EXTERNAL_SERVICES, getExternal, saveExternal, type ExternalServiceId, type ExternalView } from '../../api/external'
import { errorText } from '../../api/client'
import { DOCS_URL } from '../../brand'
import { useT, type Key } from '../../i18n'
import { GroupedSection, ListRow } from '../../ui/List'
import { Toggle } from '../../ui/Toggle'
import m from './Manage.module.css'

/** Each service: its name, its site and the variable that locks it. */
export const SERVICE_INFO: Record<ExternalServiceId, { name: Key; hint: Key; site: string; siteLabel: string; env: string }> = {
  wikidata: { name: 'ext.wikidata', hint: 'ext.wikidata.hint', site: 'https://www.wikidata.org', siteLabel: 'wikidata.org', env: 'VAULT_WIKIDATA_BASEURL' },
  imdb: { name: 'ext.imdb', hint: 'ext.imdb.hint', site: 'https://developer.imdb.com/non-commercial-datasets/', siteLabel: 'developer.imdb.com', env: 'VAULT_IMDB_RATINGS_URL' },
  lyrics: { name: 'ext.lyrics', hint: 'ext.lyrics.hint', site: 'https://lrclib.net', siteLabel: 'lrclib.net', env: 'VAULT_LRCLIB_URL' },
  segments: { name: 'ext.segments', hint: 'ext.segments.hint', site: 'https://theintrodb.org', siteLabel: 'theintrodb.org', env: 'VAULT_SEGMENTS_ONLINE' },
}

export interface ServiceRow {
  id: ExternalServiceId
  on: boolean
  locked?: boolean
}

/** The rows of the services: a switch each (a locked one shows why it cannot change). */
export function ExternalServiceRows({ rows, onChange, disabled }: { rows: ServiceRow[]; onChange: (id: ExternalServiceId, on: boolean) => void; disabled?: boolean }) {
  const t = useT()
  return (
    <>
      {rows.map((r) => {
        const info = SERVICE_INFO[r.id]
        const name = t(info.name)
        return (
          <ListRow
            key={r.id}
            id={`ext-${r.id}`}
            label={name}
            hint={
              <>
                {t(info.hint)}{' '}
                <a href={info.site} target="_blank" rel="noreferrer">
                  {info.siteLabel}
                </a>
              </>
            }
            status={r.locked ? t(r.on ? 'ext.lockedOn' : 'ext.lockedOff', { env: info.env }) : undefined}
          >
            <Toggle label={name} checked={r.on} disabled={disabled || r.locked} onChange={(on) => onChange(r.id, on)} />
          </ListRow>
        )
      })}
    </>
  )
}

export function ExternalServicesSection() {
  const t = useT()
  const [view, setView] = useState<ExternalView | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    const ac = new AbortController()
    getExternal(ac.signal).then(setView, (e: unknown) => !ac.signal.aborted && setError(errorText(e, t('ext.loadFailed'))))
    return () => ac.abort()
  }, [t])
  const change = async (id: ExternalServiceId, on: boolean) => {
    if (!view) return
    // the switch moves at once; the server's answer is what holds
    setView({ ...view, services: view.services.map((s) => (s.id === id ? { ...s, on } : s)) })
    setBusy(true)
    try {
      setView(await saveExternal({ services: { [id]: on } }))
      setError('')
    } catch (e) {
      setView(view)
      setError(errorText(e, t('ext.saveFailed')))
    } finally {
      setBusy(false)
    }
  }
  const rows = view ? EXTERNAL_SERVICES.map((id) => view.services.find((s) => s.id === id)).filter((s): s is NonNullable<typeof s> => Boolean(s)) : []
  return (
    <>
      {error && (
        <div className={m.problem} role="alert">
          {error}
        </div>
      )}
      <GroupedSection
        footer={
          <>
            {t('ext.footer')}{' '}
            <a href={`${DOCS_URL}configuration.html#external-services`} target="_blank" rel="noreferrer">
              {t('ext.docs')}
            </a>
          </>
        }
      >
        {view ? (
          <ExternalServiceRows rows={rows} onChange={(id, on) => void change(id, on)} disabled={busy} />
        ) : (
          !error && <ListRow label={<span className={m.rowHint}>{t('common.loading')}</span>} />
        )}
      </GroupedSection>
    </>
  )
}
