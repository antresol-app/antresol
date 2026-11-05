// The names of the services an import job can come from (the job's heading).
import type { ImportServiceId } from '../../../api/types'
import { t as tr, type Key } from '../../../i18n'

/** What the file input takes (every kind the server reads). */
export const IMPORT_ACCEPT = '.csv,.m3u,.m3u8,.xml,.zip'

/** Service names: brand names stay as they are; the rest are catalog keys. */
const SERVICE_NAMES: Record<ImportServiceId, string | { key: Key }> = {
  spotify: 'Spotify',
  apple: 'Apple Music',
  yandex: { key: 'import.service.yandex' },
  youtube: 'YouTube Music',
  deezer: 'Deezer',
  file: { key: 'import.service.file' },
}

/** A service's name in the interface language (the id when unknown). */
export function serviceName(id: string): string {
  const n = SERVICE_NAMES[id as ImportServiceId]
  if (n === undefined) return id
  return typeof n === 'string' ? n : tr(n.key)
}
