// The counts addons put beside the navigation (their `navBadges`): each in
// its own boundary, so a count that throws takes only its badge away.
// Hidden at 0, `99+` above 99; the label is the addon's string taking {count}.
import { Link } from 'react-router'
import { AddonBoundary, resolveAddonPath, useAddonLabel, useAddonSlot } from '../addons/slots'
import type { Contribution } from '../addons/registry'
import { Icon } from './Icon'
import styles from './NavBadges.module.css'

function Badge({ entry }: { entry: Contribution<'navBadges'> }) {
  const count = entry.useCount()
  const label = useAddonLabel(entry.addonId)
  if (!(count > 0)) return null
  return (
    <Link to={resolveAddonPath(entry.addonId, entry.to)} className={`${styles.badge} glass`} aria-label={label(entry.label, { count })}>
      <Icon name={entry.icon} size={20} />
      <span className={styles.count}>{count > 99 ? '99+' : count}</span>
    </Link>
  )
}

export function NavBadges() {
  const entries = useAddonSlot('navBadges')
  return (
    <>
      {entries.map((e, i) => (
        <AddonBoundary key={`${e.addonId}:${i}`} addonId={e.addonId} fallback={null}>
          <Badge entry={e} />
        </AddonBoundary>
      ))}
    </>
  )
}
