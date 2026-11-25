// The library picker of the library pages: a filled pull-down button next to
// the page's sort control, as Photos puts «Обе медиатеки / Личная / Общая»
// in its toolbar. It names the library on screen («Общая медиатека» /
// «Моя медиатека»), so a filtered grid never passes for the whole server;
// the choice is the profile's (state/libraryScope). Nothing for a profile
// that cannot switch (a kid's, the core build).
import type { ReactNode } from 'react'
import type { LibraryScope } from '../api/types'
import { useLibraryScope } from '../state/libraryScope'
import { useT } from '../i18n'
import { Button } from '../ui/Button'
import { EmptyState } from '../ui/EmptyState'
import { Icon } from './Icon'
import { MenuItem, Menu } from '../ui/Menu'
import styles from './LibraryScopeMenu.module.css'

/** The two libraries, for this picker and the profile menu's. */
export const LIBRARY_OPTIONS = [
  { value: 'all', icon: 'people', label: 'comp.library.all', short: 'comp.library.allShort', detail: 'comp.library.allDetail' },
  { value: 'mine', icon: 'person', label: 'comp.library.mine', short: 'comp.library.mineShort', detail: 'comp.library.mineDetail' },
] as const satisfies readonly { value: LibraryScope; icon: 'people' | 'person'; label: string; short: string; detail: string }[]

export function LibraryScopeMenu() {
  const t = useT()
  const { scope, available, setScope } = useLibraryScope()
  if (!available) return null
  const cur = LIBRARY_OPTIONS.find((o) => o.value === scope) ?? LIBRARY_OPTIONS[0]
  return (
    <Menu
      label={t('comp.library.label')}
      placement="bottom-end"
      trigger={(tp) => (
        <button {...tp} type="button" className={styles.trigger} data-scope={scope} aria-label={`${t('comp.library.label')}: ${t(cur.label)}`}>
          <Icon name={cur.icon} size={18} />
          <span className={styles.long}>{t(cur.label)}</span>
          <span className={styles.short} aria-hidden>
            {t(cur.short)}
          </span>
          <Icon name="chevronDown" size={12} strokeWidth={2.4} className={styles.caret} />
        </button>
      )}
    >
      {(close) =>
        LIBRARY_OPTIONS.map((o) => (
          <MenuItem
            key={o.value}
            label={t(o.label)}
            detail={t(o.detail)}
            icon={<Icon name={o.icon} size={19} />}
            checked={o.value === scope}
            onSelect={() => {
              setScope(o.value)
              close()
            }}
          />
        ))
      }
    </Menu>
  )
}

/** The end of a library page's header: the picker, then the page's own controls (its sort). */
export function LibraryTools({ children }: { children?: ReactNode }) {
  return (
    <div className={styles.tools}>
      <LibraryScopeMenu />
      {children}
    </div>
  )
}

/**
 * What a page shows when «Моя медиатека» has nothing for it: one tap back
 * to the shared library, as the kit's empty state. `inset`: the page has no
 * gutters of its own. `className`: a page that lays the state out its own way
 * (Home's, over the whole window) draws it in that class instead.
 */
export function MineEmpty({ className, heading: H = 'h2', inset }: { className?: string; heading?: 'h1' | 'h2'; inset?: boolean }) {
  const t = useT()
  const { setScope } = useLibraryScope()
  const icon = <Icon name="person" size={48} strokeWidth={1.4} />
  const action = (
    <Button variant="secondary" icon="people" onClick={() => setScope('all')}>
      {t('comp.library.showAll')}
    </Button>
  )
  if (!className) return <EmptyState inset={inset} icon={icon} title={t('comp.library.mineEmpty')} hint={t('comp.library.mineEmptyHint')} actions={action} />
  return (
    <div className={className}>
      {icon}
      <H>{t('comp.library.mineEmpty')}</H>
      <p>{t('comp.library.mineEmptyHint')}</p>
      {action}
    </div>
  )
}
