// Names of a channel's tags: countries (the browser's names, and the
// countries that no longer exist, which it names oddly or not at all),
// original languages and TMDB's themes (English keywords; the common ones
// in Russian).
import { getLanguage } from '../../i18n'

const GONE: Record<string, { ru: string; en: string }> = {
  SU: { ru: 'СССР', en: 'USSR' },
  XC: { ru: 'Чехословакия', en: 'Czechoslovakia' },
  YU: { ru: 'Югославия', en: 'Yugoslavia' },
  DD: { ru: 'ГДР', en: 'East Germany' },
  XG: { ru: 'ФРГ', en: 'West Germany' },
}

const display = new Map<string, Intl.DisplayNames | null>()
function names(type: 'region' | 'language'): Intl.DisplayNames | null {
  const key = `${getLanguage()}:${type}`
  if (!display.has(key)) {
    try {
      display.set(key, new Intl.DisplayNames([getLanguage()], { type }))
    } catch {
      display.set(key, null)
    }
  }
  return display.get(key) ?? null
}

const capital = (s: string) => (s ? s[0]!.toLocaleUpperCase() + s.slice(1) : s)

export function countryName(code: string): string {
  const c = code.toUpperCase()
  const gone = GONE[c]
  if (gone) return getLanguage() === 'ru' ? gone.ru : gone.en
  try {
    return names('region')?.of(c) ?? c
  } catch {
    return c
  }
}

export function languageName(code: string): string {
  try {
    return capital(names('language')?.of(code) ?? code)
  } catch {
    return code
  }
}

// TMDB's common themes in Russian (the rest stay as TMDB names them)
const THEMES_RU: Record<string, string> = {
  space: 'космос', christmas: 'Новый год и Рождество', zombie: 'зомби', vampire: 'вампиры', superhero: 'супергерои',
  'time travel': 'путешествия во времени', dinosaur: 'динозавры', pirate: 'пираты', robot: 'роботы', shark: 'акулы',
  heist: 'ограбление', spy: 'шпионы', sport: 'спорт', alien: 'пришельцы', 'based on novel or book': 'по книге',
  'based on true story': 'основано на реальных событиях', 'world war ii': 'Вторая мировая', 'world war i': 'Первая мировая',
  'biography': 'биография', 'dystopia': 'антиутопия', 'post-apocalyptic future': 'постапокалипсис', 'serial killer': 'серийный убийца',
  'artificial intelligence (a.i.)': 'искусственный интеллект', 'magic': 'магия', 'friendship': 'дружба', 'love': 'любовь',
  'revenge': 'месть', 'murder': 'убийство', 'police': 'полиция', 'high school': 'школа', 'family': 'семья', 'music': 'музыка',
  'animals': 'животные', 'dog': 'собаки', 'cat': 'кошки', 'fairy tale': 'сказка', 'remake': 'ремейк', 'sequel': 'продолжение',
  'superhero team': 'команда супергероев', 'martial arts': 'боевые искусства', 'new york city': 'Нью-Йорк', 'paris, france': 'Париж',
  'london, england': 'Лондон', 'moscow, russia': 'Москва', 'soviet union': 'СССР', 'cold war': 'холодная война', 'war': 'война',
  'woman director': 'режиссёр-женщина', 'duringcreditsstinger': 'сцена после титров', 'aftercreditsstinger': 'сцена после титров',
  'anime': 'аниме', 'based on comic': 'по комиксу', 'road trip': 'путешествие', 'christmas party': 'новогодняя вечеринка',
}

export function themeName(key: string): string {
  return capital(getLanguage() === 'ru' ? (THEMES_RU[key] ?? key) : key)
}
