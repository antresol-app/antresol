// «Язык интерфейса» chosen in the web app: it applies here at once and is
// saved for the profile (PUT /api/me/ui-language), so the profile's other
// apps follow; «Как в системе» clears the profile's choice — the interface
// then follows the profile's own language (audio languages, the
// household's), else the browser.
import { putUILanguage } from '../api/client'
import { setLanguagePref, type LangPref } from '../i18n'
import { syncProfileLanguage } from './session'

export function chooseInterfaceLanguage(v: LangPref) {
  setLanguagePref(v)
  putUILanguage(v === 'system' ? '' : v).then(syncProfileLanguage, () => undefined)
}
