// Number words and byte/speed formatting shared by the admin screens.
import { formatSize } from '../components/ActionBar'
import type { TorrentStatus } from '../api/types'
import { locale, t } from '../i18n'

export { formatSize }

/**
 * Russian plural form: plural(5, ['файл', 'файла', 'файлов']) → «файлов».
 * Interface text uses catalog plural entries (`t(key, { count })`) instead.
 */
export function plural(n: number, forms: [string, string, string]): string {
  const m10 = Math.abs(n) % 10
  const m100 = Math.abs(n) % 100
  if (m10 === 1 && m100 !== 11) return forms[0]
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return forms[1]
  return forms[2]
}

export const count = (n: number, forms: [string, string, string]) => `${n} ${plural(n, forms)}`

/** Bytes per second as «2,4 МБ/с» / "2.4 MB/s". */
export function formatRate(bps: number): string {
  return t('common.perSecond', { size: formatSize(Math.max(0, bps)) })
}

/** A download's status as the downloads screen says it. */
export function torrentStatusLabel(status: TorrentStatus, mode?: string): string {
  switch (status) {
    case 'queued':
      return t('common.download.queued')
    case 'downloading':
      return mode === 'stream' ? t('common.download.streaming') : t('common.download.downloading')
    case 'seeding':
      return __LIBRARY_EDITION__ ? status : t('common.download.seeding')
    case 'paused':
      return t('common.download.paused')
    case 'waiting_space':
      return t('common.download.waitingSpace')
    case 'error':
      return t('common.error')
    default:
      return status
  }
}

/** «5 мин назад», «вчера», «12 марта». */
export function ago(iso: string | undefined | null, now = Date.now()): string {
  if (!iso) return ''
  const at = Date.parse(iso)
  if (Number.isNaN(at)) return ''
  const s = Math.max(0, (now - at) / 1000)
  if (s < 60) return t('common.ago.now')
  if (s < 3600) return t('common.ago.minutes', { n: Math.floor(s / 60) })
  if (s < 86400) return t('common.ago.hours', { n: Math.floor(s / 3600) })
  if (s < 2 * 86400) return t('common.ago.yesterday')
  return new Date(at).toLocaleDateString(locale(), { day: 'numeric', month: 'long', year: new Date(at).getFullYear() === new Date(now).getFullYear() ? undefined : 'numeric' })
}
