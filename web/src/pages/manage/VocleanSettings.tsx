// «Настройки сервера → Обработка → Очищенные озвучки» (what the server makes
// of the files, not what the library holds): «Обработать всю медиатеку» (the backlog runs even while something
// plays, at the lowest priority) and «Перепроверить неподходящие» (the not
// eligible and given-up tracks measured again). How many are cleaned,
// waiting, failed or not eligible is in «Задачи» with the other background
// work (pages/manage/Tasks.tsx); a row leads there. Its own endpoint
// (/api/admin/voclean), not the settings object.
import { useState } from 'react'
import { useNavigate } from 'react-router'
import { errorText, getVoclean, setVocleanBackfill } from '../../api/client'
import type { VocleanStatus } from '../../api/types'
import { useT } from '../../i18n'
import { usePolling } from '../../state/usePolling'
import { Button } from '../../ui/Button'
import { GroupedSection, ListLinkRow, ListRow } from '../../ui/List'
import { Toggle } from '../../ui/Toggle'
import { Spinner } from '../../ui/Spinner'
import m from './Manage.module.css'

/** How often the counts are read again while the section is open. */
export const VOCLEAN_POLL_MS = 10_000

export function VocleanSection() {
  const t = useT()
  const navigate = useNavigate()
  const [st, setSt] = useState<VocleanStatus | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState<'' | 'backfill' | 'recheck'>('')

  usePolling(
    (signal) =>
      getVoclean(signal).then(
        (v) => {
          setSt(v)
          setError('')
        },
        (e: unknown) => !signal.aborted && setError(errorText(e, t('settings.voclean.loadFailed'))),
      ),
    VOCLEAN_POLL_MS,
  )

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
              <Spinner track className={m.spinner} />
            </span>
          )}
        </ListRow>
      </GroupedSection>
    )
  }

  return (
    <GroupedSection title={t('settings.voclean.group')} footer={t('settings.voclean.footer')}>
      {error && (
        <ListRow label={t('settings.voclean.group')} status={<span className={m.problem}>{error}</span>} />
      )}
      <ListLinkRow label={t('settings.voclean.progress')} icon="updates" tint="orange" onOpen={() => navigate('/manage/tasks')} />
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
      <ListRow label={t('settings.voclean.recheck')}>
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
