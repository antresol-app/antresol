// «Настройки сервера → Медиатека → Очищенные озвучки»: how many voice-overs
// are cleaned, waiting, failed or not eligible (updated while the page is
// open), «Обработать всю медиатеку» (the backlog runs even while something
// plays, at the lowest priority) and «Перепроверить неподходящие» (the not
// eligible and given-up tracks measured again). Its own endpoint
// (/api/admin/voclean), not the settings object.
import { useEffect, useState } from 'react'
import { errorText, getVoclean, setVocleanBackfill } from '../../api/client'
import type { VocleanStatus } from '../../api/types'
import { useT } from '../../i18n'
import { Button } from '../../ui/Button'
import { GroupedSection, ListRow } from '../../ui/List'
import { Toggle } from '../../ui/Toggle'
import m from './Manage.module.css'

/** How often the counts are read again while the section is open. */
export const VOCLEAN_POLL_MS = 10_000

export function VocleanSection() {
  const t = useT()
  const [st, setSt] = useState<VocleanStatus | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState<'' | 'backfill' | 'recheck'>('')

  useEffect(() => {
    const ac = new AbortController()
    const load = () =>
      getVoclean(ac.signal).then(
        (v) => {
          setSt(v)
          setError('')
        },
        (e: unknown) => !ac.signal.aborted && setError(errorText(e, t('settings.voclean.loadFailed'))),
      )
    void load()
    const id = window.setInterval(() => void load(), VOCLEAN_POLL_MS)
    return () => {
      ac.abort()
      window.clearInterval(id)
    }
  }, [t])

  const send = async (on: boolean, recheck: boolean) => {
    setBusy(recheck ? 'recheck' : 'backfill')
    try {
      setSt(await setVocleanBackfill({ on, recheck }))
      setError('')
    } catch (e) {
      setError(errorText(e, t('settings.voclean.saveFailed')))
    } finally {
      setBusy('')
    }
  }

  if (!st) {
    return (
      <GroupedSection title={t('settings.voclean.group')}>
        <ListRow label={t('settings.voclean.progress')}>
          {error ? (
            <span className={m.problem} role="alert">
              {error}
            </span>
          ) : (
            <span className={m.spinnerRow} role="status">
              <span className={m.spinner} aria-hidden />
            </span>
          )}
        </ListRow>
      </GroupedSection>
    )
  }

  return (
    <GroupedSection title={t('settings.voclean.group')} footer={t('settings.voclean.footer')}>
      <ListRow
        label={t('settings.voclean.progress')}
        status={
          error ||
          t('settings.voclean.counts', { done: st.done, queued: st.queued, failed: st.failed, notEligible: st.notEligible })
        }
      />
      <ListRow label={t('settings.voclean.backfill')} hint={t('settings.voclean.backfill.hint')}>
        <Toggle
          label={t('settings.voclean.backfill')}
          checked={st.backfill}
          disabled={busy !== ''}
          onChange={(on) => {
            setSt({ ...st, backfill: on })
            void send(on, false)
          }}
        />
      </ListRow>
      <ListRow label={t('settings.voclean.recheck')} hint={t('settings.voclean.recheck.hint')}>
        <Button
          size="small"
          variant="secondary"
          icon="restart"
          loading={busy === 'recheck'}
          disabled={busy !== '' || st.notEligible + st.failed === 0}
          onClick={() => void send(st.backfill, true)}
        >
          {t('settings.voclean.recheck.button')}
        </Button>
      </ListRow>
    </GroupedSection>
  )
}
