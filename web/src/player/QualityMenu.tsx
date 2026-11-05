// «Качество»: Авто / Оригинал / 1080p / 720p / 480p. «Авто» is the original at
// home and «Качество вне дома» (Настройки) outside it. A choice restarts the
// video at the same place.
import type { OnlineQualityInfo, Quality, QualityInfo } from '../api/types'
import { MenuHeading, MenuItem, Menu } from '../ui/Menu'
import { t, useT } from '../i18n'
import styles from './QualityMenu.module.css'

// labels are getters: read at render time in the interface language
export const QUALITIES: { value: Quality; readonly label: string }[] = [
  { value: 'auto', get label() { return t('common.auto') } },
  { value: 'original', get label() { return t('common.original') } },
  { value: '1080', label: '1080p' },
  { value: '720', label: '720p' },
  { value: '480', label: '480p' },
]

const label = (q: Quality) => QUALITIES.find((x) => x.value === q)?.label ?? t('common.auto')

/** What «Авто» means right now, in plain words. */
export function autoDetail(info: QualityInfo | null): string {
  if (!info) return t('player.quality.picking')
  if (info.applied === 'original') return info.remote ? t('player.quality.asInFile') : t('player.quality.homeAsInFile')
  return t('player.quality.away', { label: label(info.applied) })
}

export interface QualityMenuProps {
  quality: Quality
  info: QualityInfo | null
  onChange: (q: Quality) => void
  onOpenChange?: (open: boolean) => void
}

export function QualityMenu({ quality, info, onChange, onOpenChange }: QualityMenuProps) {
  const t = useT()
  const shown = quality === 'auto' && info && info.applied !== 'original' ? t('player.quality.autoApplied', { label: label(info.applied) }) : label(quality)
  return (
    <Menu
      label={t('common.quality')}
      placement="top-end"
      className={styles.menu}
      onOpenChange={onOpenChange}
      trigger={(p) => (
        <button {...p} type="button" className={styles.trigger} aria-label={t('player.quality.aria', { shown })} title={t('common.quality')}>
          <span className={styles.pill}>{shown}</span>
        </button>
      )}
    >
      {(close) => (
        <div role="group" aria-label={t('common.quality')}>
          <MenuHeading>{t('common.quality')}</MenuHeading>
          {QUALITIES.map((q) => (
            <MenuItem
              key={q.value}
              label={q.label}
              detail={q.value === 'auto' ? autoDetail(info) : q.value === 'original' ? t('player.quality.originalDetail') : undefined}
              checked={q.value === quality}
              onSelect={() => {
                close()
                if (q.value !== quality) onChange(q.value)
              }}
            />
          ))}
        </div>
      )}
    </Menu>
  )
}

export interface OnlineQualityMenuProps {
  /** The source's qualities («1080p», «720p»…) and the one playing. */
  info: OnlineQualityInfo
  onChange: (q: string) => void
  onOpenChange?: (open: boolean) => void
}

/** «Качество» of an online stream: the qualities its source offers; a choice plays it again at the same place. */
export function OnlineQualityMenu({ info, onChange, onOpenChange }: OnlineQualityMenuProps) {
  if (info.options.length === 0) return null
  const t = useT()
  const shown = info.current || info.options[0]!
  return (
    <Menu
      label={t('common.quality')}
      placement="top-end"
      className={styles.menu}
      onOpenChange={onOpenChange}
      trigger={(p) => (
        <button {...p} type="button" className={styles.trigger} aria-label={t('player.quality.aria', { shown })} title={t('common.quality')}>
          <span className={styles.pill}>{shown}</span>
        </button>
      )}
    >
      {(close) => (
        <div role="group" aria-label={t('common.quality')}>
          <MenuHeading>{t('common.quality')}</MenuHeading>
          {info.options.map((q) => (
            <MenuItem
              key={q}
              label={q}
              checked={q === shown}
              onSelect={() => {
                close()
                if (q !== shown) onChange(q)
              }}
            />
          ))}
        </div>
      )}
    </Menu>
  )
}
