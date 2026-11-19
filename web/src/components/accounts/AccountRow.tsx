// The first row of every account's card (pages/AccountsSettings.tsx): whether
// the profile is linked, and as whom; what flows which way, in one honest
// line; when the last sync ran and how it went; «Отвязать» when linked. The
// card's other rows (the link form, a switch, a file) follow it.
import type { ReactNode } from 'react'
import { useT } from '../../i18n'
import { Button } from '../../ui/Button'
import { ListRow } from '../../ui/List'

export function AccountRow({ linked, name, direction, status, busy, onUnlink }: {
  linked: boolean
  /** The account's name on the service (shown when linked). */
  name: string
  /** What syncs which way, one line. */
  direction: ReactNode
  /** The last sync: when and the result (an error says so); only when linked. */
  status?: ReactNode
  busy: boolean
  onUnlink: () => void
}) {
  const t = useT()
  return (
    <ListRow label={linked ? t('acct.linkedAs', { name }) : t('acct.notLinked')} hint={direction} status={linked ? status : undefined}>
      {linked && (
        <Button variant="secondary" size="small" disabled={busy} onClick={onUnlink}>
          {t('acct.unlink')}
        </Button>
      )}
    </ListRow>
  )
}
