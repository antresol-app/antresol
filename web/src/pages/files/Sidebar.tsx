// Finder sidebar: «Медиатека» (Фильмы, Сериалы, Музыка, Книги), «Места»
// (Загрузки, Кеш просмотра, Весь диск, Корзина — the last two for the admin)
// and the storage bar at the bottom. On a phone the same groups make the
// iOS «Файлы» «Обзор» screen.
import { NavLink } from 'react-router'
import type { FileRoot } from '../../api/types'
import { Glyph, rootGlyph, type GlyphName } from './glyphs'
import { StorageBar } from './StorageBar'
import styles from './Sidebar.module.css'
import { t, useT } from '../../i18n'

const LIBRARY = ['movies', 'series', 'music', 'books']

interface Link {
  id: string
  title: string
  icon: GlyphName
  to: string
}

export function groups(roots: FileRoot[], isAdmin: boolean): { title: string; links: Link[] }[] {
  const link = (r: FileRoot): Link => ({ id: r.id, title: r.title, icon: rootGlyph(r.icon), to: `/files/${encodeURIComponent(r.id)}` })
  const lib = LIBRARY.map((id) => roots.find((r) => r.id === id)).filter((r): r is FileRoot => Boolean(r)).map(link)
  const places = roots.filter((r) => !LIBRARY.includes(r.id) && r.id !== 'library').map(link)
  if (isAdmin) places.push({ id: 'trash', title: t('files.trash'), icon: 'trash', to: '/files/trash' })
  return [
    { title: t('common.library'), links: lib },
    { title: t('files.places'), links: places },
  ].filter((g) => g.links.length > 0)
}

export interface SidebarProps {
  roots: FileRoot[]
  isAdmin: boolean
  /** Phone «Обзор» screen. */
  overview?: boolean
  usageRefresh?: number
}

export function Sidebar({ roots, isAdmin, overview, usageRefresh }: SidebarProps) {
  useT()
  const gs = groups(roots, isAdmin)
  if (overview) {
    return (
      <nav className={styles.overview} aria-label={t('files.places')}>
        {gs.map((g) => (
          <section key={g.title} className={styles.ogroup} aria-label={g.title}>
            <h2 className={styles.otitle}>{g.title}</h2>
            <ul className={styles.olist}>
              {g.links.map((l) => (
                <li key={l.id}>
                  <NavLink to={l.to} className={styles.orow}>
                    <span className={`${styles.oicon} ${l.id === 'trash' ? styles.oiconTrash : ''}`}>
                      <Glyph name={l.icon} size={20} strokeWidth={1.9} />
                    </span>
                    <span className={styles.oname}>{l.title}</span>
                    <Glyph name="chevronRight" size={13} strokeWidth={2.4} className={styles.ochev} />
                  </NavLink>
                </li>
              ))}
            </ul>
          </section>
        ))}
        <StorageBar large refresh={usageRefresh} />
      </nav>
    )
  }
  return (
    <nav className={styles.sidebar} aria-label={t('files.places')}>
      <div className={styles.scroll}>
        {gs.map((g) => (
          <section key={g.title} className={styles.group} aria-label={g.title}>
            <h2 className={styles.title}>{g.title}</h2>
            {g.links.map((l) => (
              <NavLink key={l.id} to={l.to} className={({ isActive }) => `${styles.link} ${isActive ? styles.active : ''}`}>
                <Glyph name={l.icon} size={17} strokeWidth={1.9} className={styles.glyph} />
                <span className={styles.name}>{l.title}</span>
              </NavLink>
            ))}
          </section>
        ))}
      </div>
      <div className={styles.storage}>
        <StorageBar refresh={usageRefresh} />
      </div>
    </nav>
  )
}
