// «Любимые озвучки»: voice-over studios in the household's order, best
// first — a RankedList (drag or ↑/↓ on the handle, − takes one out); its
// «Добавить студию» menu offers the known studios and a field for any name.
import { useT } from '../i18n'
import { RankedList } from './RankedList'

/**
 * The studios every build knows, in suggestion order, with their other
 * spellings — a copy of the alias table of internal/release (studios.go); a
 * Go test keeps the two equal.
 */
export const STUDIOS: [name: string, aliases: string[]][] = [
  ['LostFilm', ['lost film', 'лостфильм', 'лост фильм']],
  ['Кубик в Кубе', ['kubik v kube', 'kubik3', 'кубик³', 'kubik³', 'кубик3']],
  ['NewStudio', ['new studio']],
  ['AlexFilm', ['alex film']],
  ['Jaskier', ['яськьер']],
  ['TVShows', ['tv shows']],
  ['Кураж-Бамбей', ['кураж бамбей', 'kurazh-bambey', 'kurazh bambey']],
  ['Red Head Sound', ['rhs', 'redheadsound']],
  ['BaibaKo', ['байбако']],
  ['Pifagor', ['пифагор']],
  ['Novamedia', ['nova media']],
  ['SDI Media', ['sdimedia']],
  ['Amedia', []],
  ['Coldfilm', ['cold film']],
  ['IdeaFilm', ['idea film']],
]

/** The known studios in suggestion order (the core's own of internal/release KnownStudios; any other can be typed). */
export const KNOWN_STUDIOS = STUDIOS.map(([name]) => name)

/** The server's limits (internal/releaseprefs CleanStudios). */
export const MAX_STUDIOS = 30
export const MAX_STUDIO_LENGTH = 40

/** As the server folds a studio name: lower case, ё = е, letters and digits only. */
const fold = (s: string) =>
  s
    .toLowerCase()
    .replaceAll('ё', 'е')
    .replace(/[^\p{L}\p{N}]/gu, '')

/** Folded spelling → canonical name, every spelling of every known studio. */
const CANONICAL = new Map(STUDIOS.flatMap(([name, aliases]) => [name, ...aliases].map((a) => [fold(a), name] as const)))

/** The canonical name of a known studio's name or alias; undefined when unknown. */
export const canonicalStudio = (name: string) => CANONICAL.get(fold(name))

/** What identifies a studio: its canonical name, else the folded text (as the server's StudioKey). */
const studioKey = (name: string) => canonicalStudio(name) ?? fold(name)

/** `list` with `name` at the end: trimmed, a known studio by its canonical name, never twice, at most MAX_STUDIOS. */
export function addStudio(list: string[], name: string): string[] {
  const k = studioKey(name)
  if (!fold(name) || list.length >= MAX_STUDIOS || list.some((s) => studioKey(s) === k)) return list
  return [...list, canonicalStudio(name) ?? name.trim()]
}

export function StudioList({ labelledBy, studios, onChange }: { labelledBy: string; studios: string[]; onChange: (studios: string[]) => void }) {
  const t = useT()
  const picked = new Set(studios.map(studioKey))
  // typed text filters by any spelling («kubik» → Кубик в Кубе)
  const choices = (text: string) => {
    const q = fold(text)
    return STUDIOS.filter(([name, aliases]) => !picked.has(name) && (!q || [name, ...aliases].some((a) => fold(a).includes(q)))).map(([name]) => ({ value: name, label: name }))
  }
  return (
    <RankedList
      labelledBy={labelledBy}
      items={studios}
      max={MAX_STUDIOS}
      onChange={onChange}
      choices={choices}
      onAdd={(name) => {
        const next = addStudio(studios, name)
        if (next !== studios) onChange(next)
      }}
      addLabel={t('settings.studios.add')}
      field={{ label: t('settings.studios.name'), maxLength: MAX_STUDIO_LENGTH }}
      empty={t('settings.studios.empty')}
    />
  )
}
