// The current choice of a screen of «Настройки сервера», dimmed at the
// trailing edge of its row in «Управление»'s list (iOS: «Просмотр 1080p»).
// Its own small module: the list (Manage.tsx) is also drawn around the
// profile's own settings, which must not load the settings screens.
import type { Settings as Values } from '../../api/types'
import { langLabel } from '../../components/langs'
import { t } from '../../i18n'

/** The languages a server has before its admin chooses. */
export const DEFAULT_LANGS = ['ru', 'en'] as const

/** `group`: a screen's id (library, watching, …); '' for one without a summary. */
export function settingsSummary(group: string, v: Values): string {
  switch (group) {
    case 'library':
      return (v.languages?.length ? v.languages : DEFAULT_LANGS).map((c) => langLabel(c)).join(', ')
    case 'watching':
      return v.remoteQuality === 'original' ? t('common.original') : `${v.remoteQuality ?? '1080'}p`
    case 'music':
      return t(`settings.remoteMusic.${v.remoteMusic ?? 'high'}`)
    case 'subtitles':
      return v.subtitlesAvailable && v.autoSubtitles ? t('settings.summary.auto') : ''
    case 'kindle':
      return v.mail?.host && v.mail.from ? t('settings.summary.on') : t('settings.summary.off')
    case 'transcoding':
      return v.transcodingName === 'software' ? t('settings.summary.cpu') : (v.transcodingName ?? '')
    case 'ai':
      return v.ai?.url ? v.ai.model : v.aiFromEnv ? v.aiFromEnv : t('settings.summary.offAI')
    default:
      return ''
  }
}
