// «Медиа» on a phone: the tab bar has room for five tabs, so Музыка and Книги
// (with audiobooks and comics) share one, and this switch over their pages
// moves between them. Wide screens have both in the top pill and no switch.
import { useNavigate } from 'react-router'
import { MEDIA_ROOTS, type MediaRoot } from '../app/sections'
import { useT } from '../i18n'
import { useIsCompact } from '../state/useMediaQuery'
import { SegmentedControl } from '../ui/SegmentedControl'
import styles from './MediaSwitch.module.css'

const LABEL = { '/music': 'common.music', '/books': 'common.books' } as const

export function MediaSwitch({ value }: { value: MediaRoot }) {
  const t = useT()
  const compact = useIsCompact()
  const navigate = useNavigate()
  if (!compact) return null
  return (
    <div className={styles.switch}>
      <SegmentedControl<MediaRoot>
        label={t('common.media')}
        value={value}
        onChange={(to) => navigate(to)}
        segments={MEDIA_ROOTS.map((r) => ({ value: r, label: t(LABEL[r]) }))}
      />
    </div>
  )
}
