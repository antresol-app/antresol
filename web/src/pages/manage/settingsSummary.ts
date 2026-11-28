// The current choice of a screen of «Настройки сервера», dimmed at the
// trailing edge of its row in «Управление»'s list (iOS: «Просмотр 1080p»).
// Its own small module: the list (Manage.tsx) is also drawn around the
// profile's own settings, which must not load the settings screens.
import type { Settings as Values } from '../../api/types'
import { t } from '../../i18n'

/** The languages a server has before its admin chooses. */
export const DEFAULT_LANGS = ['ru', 'en'] as const

/** «ИИ-помощник»: the first model that is on, or «Выключено» (the assistant off, or no model to ask). */
function aiSummary(v: Values): string {
  const first = v.ai?.off ? undefined : v.ai?.list.find((e) => e.kind !== 'speech' && !e.off && e.status?.state !== 'noKey')
  return first ? first.model : t('settings.summary.offAI')
}

/** `group`: a screen's id (library, watching, …); '' for one without a summary. */
export function settingsSummary(group: string, v: Values): string {
  switch (group) {
    case 'library':
      return v.metadataLanguage ? t(`common.uiLanguage.${v.metadataLanguage}`) : ''
    case 'watching':
      return v.remoteQuality === 'original' ? t('common.original') : `${v.remoteQuality ?? '1080'}p`
    case 'music':
      return t(`settings.remoteMusic.${v.remoteMusic ?? 'high'}`)
    case 'subtitles':
      return v.subtitlesAvailable && v.autoSubtitles ? t('settings.summary.auto') : ''
    case 'kindle':
      return v.mail?.host && v.mail.from ? t('settings.summary.on') : t('settings.summary.off')
    case 'processing':
      return v.transcodingName === 'software' ? t('settings.summary.cpu') : (v.transcodingName ?? '')
    case 'ai':
      return aiSummary(v)
    default:
      return ''
  }
}
