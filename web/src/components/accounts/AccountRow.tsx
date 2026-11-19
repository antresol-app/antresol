// The first row of every account's card (pages/AccountsSettings.tsx): whether
// the profile is linked, and as whom; what flows which way, in one honest
// line; when the last sync ran and how it went (or that it runs now);
// «Синхронизировать» and «Отвязать» when linked. The card's other rows
// (the link form, a switch, a file) follow it.
import type { ReactNode } from 'react'
import { useT } from '../../i18n'
import { Button } from '../../ui/Button'
import { ListRow } from '../../ui/List'

export function AccountRow({ linked, name, direction, status, syncing, busy, onSync, onUnlink }: {
  linked: boolean
  /** The account's name on the service (shown when linked). */
  name: string
  /** What syncs which way, one line. */
  direction: ReactNode
  /** The last sync: when and the result (an error says so); only when linked. */
  status?: ReactNode
  /** The server is syncing the account now: the button spins and the status says so. */
  syncing: boolean
  busy: boolean
  onSync: () => void
  onUnlink: () => void
}) {
  const t = useT()
  return (
    // stack: on a phone the two actions go under the text, not squeeze it
    <ListRow stack label={linked ? t('acct.linkedAs', { name }) : t('acct.notLinked')} hint={direction} status={linked ? (syncing ? t('acct.status.syncing') : status) : undefined}>
      {linked && (
        <>
          <Button variant="secondary" size="small" loading={syncing} disabled={busy} onClick={onSync}>
            {t('acct.sync')}
          </Button>
          <Button variant="secondary" size="small" disabled={busy} onClick={onUnlink}>
            {t('acct.unlink')}
          </Button>
        </>
      )}
    </ListRow>
  )
}
