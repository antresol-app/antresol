// «ТВ» of a big list (an IPTV playlist: hundreds of channels in tens of
// groups), its first level: «Избранное», «Все каналы», then the categories
// (the AI helper's topics, the sources, the playlist's groups), each with
// how many channels it has. A row is a link (?f=…): the second level is the
// channels of the one chosen, and Back — from it, or from the player —
// returns here.
import type { ChannelCategory } from '../../api/channels'
import { useT } from '../../i18n'
import { GroupedSection, ListLinkRow } from '../../ui/List'
import styles from './Tv.module.css'

const KINDS = ['ai', 'source', 'group'] as const

export function ChannelCategories({ categories, all, favorites, href }: { categories: ChannelCategory[]; all: number; favorites: number; href: (f: string) => string }) {
  const t = useT()
  return (
    <nav className={styles.categories} aria-label={t('tv.categories')}>
      <GroupedSection>
        <ListLinkRow label={t('tv.filterFav')} value={String(favorites)} to={href('fav')} />
        <ListLinkRow label={t('tv.allChannels')} value={String(all)} to={href('all')} />
      </GroupedSection>
      {KINDS.map((kind) => {
        const list = categories.filter((c) => c.kind === kind)
        if (!list.length) return null
        return (
          <GroupedSection key={kind} title={t(`tv.categoryGroup.${kind}`)}>
            {list.map((c) => (
              <ListLinkRow key={c.id} label={c.name} value={String(c.count)} to={href(`k:${c.id}`)} />
            ))}
          </GroupedSection>
        )
      })}
    </nav>
  )
}
