// The core build's way on from an empty library: it brings nothing in from
// Search, so an admin goes to the rescan in Settings (a section others cannot open).
import { useSession } from '../state/session'
import { useT } from '../i18n'
import { ButtonLink } from '../ui/Button'

export function RescanLink({ variant }: { variant?: 'secondary' }) {
  const t = useT()
  const { user } = useSession()
  if (!user?.isAdmin) return null
  return (
    <ButtonLink to="/manage/settings#rescan" icon="restart" variant={variant}>
      {t('browse.rescanInSettings')}
    </ButtonLink>
  )
}
