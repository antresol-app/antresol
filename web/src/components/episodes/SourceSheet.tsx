// «Выбрать источник»: every source of one episode, by language and dub. The
// current pick is checked; a pick is remembered by the caller and plays.
import type { EpisodeSource } from '../../api/types'
import { useSourceKinds } from '../../addons/slots'
import { locale, useT } from '../../i18n'
import type { SourceGroup } from '../../state/pickSource'
import { Badge } from '../../ui/Badge'
import { GroupedSection } from '../../ui/List'
import { Sheet } from '../../ui/Sheet'
import { Icon } from '../Icon'
import { episodeTitle } from '../EpisodeList'
import styles from './episodes.module.css'

export interface SourceSheetProps {
  episode: { number: number; name: string }
  groups: SourceGroup[]
  current: EpisodeSource | null
  onPick: (src: EpisodeSource) => void
  /** «Другие источники…»; no row without it. */
  onOther?: () => void
  /** «Скачать серию» for a pinned source; no row without it. */
  onDownload?: (src: EpisodeSource) => void
  /** «На устройство» for a library source; no row without it. */
  onDownloadLibrary?: (src: EpisodeSource) => void
  onClose: () => void
}

/** The language's name, capitalised (Russian names come lowercase). */
function languageHeading(lang: string, other: string): string {
  if (!lang) return other
  let name: string
  try {
    name = new Intl.DisplayNames(locale(), { type: 'language' }).of(lang) ?? lang.toUpperCase()
  } catch {
    return lang.toUpperCase()
  }
  return name.charAt(0).toLocaleUpperCase(locale()) + name.slice(1)
}

export function SourceSheet({ episode, groups, current, onPick, onOther, onDownload, onDownloadLibrary, onClose }: SourceSheetProps) {
  const t = useT()
  const kinds = useSourceKinds()
  const name = episodeTitle({ title: episode.name, episodeNo: episode.number })
  const plain = t('comp.episode.number', { n: episode.number })
  const title = name === plain ? plain : t('episodes.sheet.title', { n: episode.number, name })
  const pinned = current?.kind === 'pinned' ? current : groups.flatMap((g) => g.dubs.flatMap((d) => d.items)).find((s) => s.kind === 'pinned')
  const library = current?.kind === 'library' ? current : groups.flatMap((g) => g.dubs.flatMap((d) => d.items)).find((s) => s.kind === 'library')
  const kindLabel = (s: EpisodeSource) => kinds.label(s.kind)
  const providerOf = (s: EpisodeSource) => s.provider || s.meta.providerId || ''
  return (
    <Sheet title={title} role="dialog" size="large" noButtons dismissOnOutside onCancel={onClose}>
      {groups.map((g) => (
        <GroupedSection key={g.lang} title={languageHeading(g.lang, t('episodes.sheet.otherLang'))}>
          {g.dubs.map((d) => [
            d.dub && (
              <li key={`dub:${d.dub}`} className={styles.dub}>
                {d.dub}
              </li>
            ),
            ...d.items.map((s) => {
              const on = s === current || (!!current && s.ref === current.ref && s.kind === current.kind)
              const provider = providerOf(s)
              return (
                <li key={`${s.kind}:${s.ref}`} className={styles.pickRow}>
                  <button type="button" className={styles.pick} aria-pressed={on} onClick={() => onPick(s)}>
                    <span className={styles.text}>
                      <span className={styles.label}>{provider ? `${provider} · ${kindLabel(s)}` : kindLabel(s)}</span>
                      <span className={styles.hint}>
                        {s.quality && <Badge>{s.quality}</Badge>}
                        {!!s.meta.subs?.length && <Badge>{t('episodes.sheet.subs')}</Badge>}
                      </span>
                    </span>
                    <span className={styles.check} aria-hidden>
                      {on && <Icon name="check" size={16} strokeWidth={2.4} />}
                    </span>
                  </button>
                </li>
              )
            }),
          ])}
        </GroupedSection>
      ))}
      {(onOther || (onDownload && pinned) || (onDownloadLibrary && library)) && (
        <GroupedSection>
          {onOther && (
            <li className={styles.pickRow}>
              <button type="button" className={styles.pick} onClick={onOther}>
                <span className={styles.text}>
                  <span className={styles.label}>{t('episodes.sheet.other')}</span>
                </span>
              </button>
            </li>
          )}
          {onDownload && pinned && (
            <li className={styles.pickRow}>
              <button type="button" className={styles.pick} onClick={() => onDownload(pinned)}>
                <span className={styles.text}>
                  <span className={styles.label}>{t('episodes.sheet.download')}</span>
                </span>
              </button>
            </li>
          )}
          {onDownloadLibrary && library && (
            <li className={styles.pickRow}>
              <button type="button" className={styles.pick} onClick={() => onDownloadLibrary(library)}>
                <span className={styles.text}>
                  <span className={styles.label}>{t('episodes.sheet.toDevice')}</span>
                </span>
              </button>
            </li>
          )}
        </GroupedSection>
      )}
    </Sheet>
  )
}
