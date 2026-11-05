// The language catalog of the settings («Мои языки», «Звук и субтитры»).
import type { LangCode } from '../api/types'
import { t } from '../i18n'

/** The language catalog (internal/lang), in the order «Языки» offers it. */
export const LANGS: LangCode[] = ['ru', 'en', 'uk', 'de', 'fr', 'es', 'it', 'pt', 'pl', 'ja', 'zh', 'ko']
/** «Русский», «Английский» … in the interface language. */
export const langLabel = (c: LangCode) => (LANGS.includes(c) ? t(`common.lang.${c}`) : c)

/** The catalog's languages not in `picked`, for an add menu. */
export const langChoices = (picked: string[]) => LANGS.filter((c) => !picked.includes(c)).map((c) => ({ value: c, label: langLabel(c) }))
