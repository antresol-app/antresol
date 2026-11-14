// «Что показывать» of the channel editor, by tags: what (movies, series),
// genres, countries, years, the original language and themes, each a row
// of chips that turn on and off (the most common first, the rest in
// «Ещё…»), and under them what such a channel would play now — how many
// titles and their posters — so an empty channel shows before it is made.
import { useEffect, useState } from 'react'
import { previewChannel, type ChannelOptions, type ChannelPreviewResult, type ChannelRules, type TagCount } from '../../api/channels'
import { useT } from '../../i18n'
import { Artwork } from '../../ui/Artwork'
import { GroupedSection, ListRow } from '../../ui/List'
import { NumberField } from '../../ui/NumberField'
import { MultiSelect } from '../../ui/Select'
import { ToggleChips } from '../../ui/ToggleChips'
import { countryName, languageName, themeName } from './tagNames'
import styles from './ChannelTags.module.css'

/** The part of the rules the tags set. */
export type TagRules = Pick<ChannelRules, 'kinds' | 'genres' | 'countries' | 'languages' | 'keywords' | 'yearFrom' | 'yearTo'>

export const tagRulesOf = (r: ChannelRules): TagRules => ({
  kinds: r.kinds,
  genres: r.genres,
  countries: r.countries,
  languages: r.languages,
  keywords: r.keywords,
  yearFrom: r.yearFrom,
  yearTo: r.yearTo,
})

/** The list with the value turned on or off (undefined when empty). */
const toggled = (list: string[] | undefined, v: string, on: boolean) => {
  const next = on ? [...(list ?? []).filter((x) => x !== v), v] : (list ?? []).filter((x) => x !== v)
  return next.length ? next : undefined
}

/**
 * A decade's chip turns a range of years on and off: the first one picks
 * it, a neighbour of the range widens it, one inside narrows the range to
 * it, the range's only decade clears it.
 */
export function decadeToggle(from: number | undefined, to: number | undefined, decade: number): { yearFrom?: number; yearTo?: number } {
  const end = decade + 9
  if (!from && !to) return { yearFrom: decade, yearTo: end }
  const f = from ?? decade
  const t = to ?? end
  if (f === decade && t === end) return {}
  if (decade + 10 === f) return { yearFrom: decade, yearTo: t }
  if (decade === Math.floor(t / 10) * 10 + 10) return { yearFrom: f, yearTo: end }
  return { yearFrom: decade, yearTo: end }
}

const decadeOn = (from: number | undefined, to: number | undefined, d: number) => (from || to ? (from ?? 0) <= d && d + 9 <= (to ?? 9999) : false)

/** The first `top` as chips, the rest in «Ещё…». */
function TagRow({
  label,
  tags,
  name,
  value,
  onChange,
  top,
}: {
  label: string
  tags: TagCount[]
  name: (key: string) => string
  value: string[] | undefined
  onChange: (v: string[] | undefined) => void
  top: number
}) {
  const t = useT()
  if (tags.length === 0) return null
  const on = value ?? []
  // a tag that is on stays a chip even when it is not among the most common
  const shown = [...tags.slice(0, top), ...tags.slice(top).filter((x) => on.includes(x.key))]
  const rest = tags.filter((x) => !shown.includes(x))
  return (
    <ListRow label={label} wide>
      <ToggleChips
        label={label}
        chips={shown.map((x) => ({ value: x.key, label: name(x.key), count: x.count || undefined }))}
        value={on}
        onToggle={(v, isOn) => onChange(toggled(value, v, isOn))}
      >
        {rest.length > 0 && (
          <MultiSelect
            label={`${label}: ${t('tv.tags.more')}`}
            value={[] as string[]}
            none={t('tv.tags.more')}
            options={rest.map((x) => ({ value: x.key, label: x.count ? `${name(x.key)} · ${x.count}` : name(x.key) }))}
            onChange={(picked) => onChange([...on, ...picked])}
            placement="bottom-start"
          />
        )}
      </ToggleChips>
    </ListRow>
  )
}

export function ChannelTags({ options, value, onChange }: { options: ChannelOptions | null; value: TagRules; onChange: (v: TagRules) => void }) {
  const t = useT()
  const set = (patch: Partial<TagRules>) => onChange({ ...value, ...patch })
  const kinds = value.kinds ?? []
  const decades = options?.decades ?? []
  const pending = options !== null && !options.countries?.length && !options.keywords?.length
  return (
    <>
      {/* what would play first: the count answers each tap in view */}
      <TagPreview rules={value} />
      <GroupedSection footer={pending ? t('tv.tags.pending') : undefined}>
        <ListRow label={t('tv.tags.kinds')} wide>
          <ToggleChips
            label={t('tv.tags.kinds')}
            chips={[
              { value: 'movie' as const, label: t('tv.rules.movies') },
              { value: 'series' as const, label: t('tv.rules.series') },
            ]}
            value={kinds}
            onToggle={(v, on) => {
              const next = on ? [...kinds.filter((k) => k !== v), v] : kinds.filter((k) => k !== v)
              // both or none: no filter
              set({ kinds: next.length === 1 ? next : undefined })
            }}
          />
        </ListRow>
        <TagRow
          label={t('tv.tags.genres')}
          tags={(options?.genres ?? []).map((g) => ({ key: g.key, count: 0 }))}
          name={(k) => options?.genres.find((g) => g.key === k)?.name ?? k}
          value={value.genres}
          onChange={(genres) => set({ genres })}
          top={40}
        />
        <TagRow label={t('tv.tags.countries')} tags={options?.countries ?? []} name={countryName} value={value.countries} onChange={(countries) => set({ countries })} top={12} />
        {decades.length > 0 && (
          <ListRow label={t('tv.tags.years')} wide>
            <ToggleChips
              label={t('tv.tags.years')}
              chips={decades.map((d) => ({ value: String(d), label: t('tv.rules.decade', { decade: d }) }))}
              value={decades.filter((d) => decadeOn(value.yearFrom, value.yearTo, d)).map(String)}
              onToggle={(v) => set(decadeToggle(value.yearFrom, value.yearTo, Number(v)))}
            />
            <div className={styles.years}>
              <NumberField
                label={t('tv.tags.from')}
                value={value.yearFrom ?? NaN}
                min={1880}
                max={2100}
                chars={4}
                onChange={(y) => set({ yearFrom: y || undefined })}
              />
              <span aria-hidden>–</span>
              <NumberField label={t('tv.tags.to')} value={value.yearTo ?? NaN} min={1880} max={2100} chars={4} onChange={(y) => set({ yearTo: y || undefined })} />
            </div>
          </ListRow>
        )}
        <TagRow label={t('tv.tags.languages')} tags={options?.languages ?? []} name={languageName} value={value.languages} onChange={(languages) => set({ languages })} top={8} />
        <TagRow label={t('tv.tags.themes')} tags={options?.keywords ?? []} name={themeName} value={value.keywords} onChange={(keywords) => set({ keywords })} top={16} />
      </GroupedSection>
    </>
  )
}

/** What a channel of the tags plays now: counts, then posters. */
function TagPreview({ rules }: { rules: TagRules }) {
  const t = useT()
  const [result, setResult] = useState<ChannelPreviewResult | null>(null)
  const [busy, setBusy] = useState(true)
  const key = JSON.stringify(rules)
  useEffect(() => {
    const ac = new AbortController()
    setBusy(true)
    // a burst of chip taps is one request
    const id = window.setTimeout(() => {
      previewChannel(JSON.parse(key) as TagRules, ac.signal).then(
        (r) => {
          setResult(r)
          setBusy(false)
        },
        () => !ac.signal.aborted && setBusy(false),
      )
    }, 250)
    return () => {
      window.clearTimeout(id)
      ac.abort()
    }
  }, [key])
  const empty = result !== null && result.movies + result.series === 0
  const parts = result ? [result.movies ? t('tv.preview.movies', { count: result.movies }) : '', result.series ? t('tv.preview.series', { count: result.series }) : ''].filter(Boolean) : []
  return (
    <GroupedSection title={t('tv.preview.label')}>
      <ListRow
        label={<span aria-live="polite">{result === null ? t('tv.preview.loading') : empty ? t('tv.preview.empty') : parts.join(' · ')}</span>}
        className={busy ? styles.busy : undefined}
        wide={!!result?.items.length}
      >
        {result && result.items.length > 0 && (
          <ul className={styles.posters} aria-label={t('tv.preview.label')}>
            {result.items.map((it) => (
              <li key={it.id} className={styles.poster} title={it.year ? `${it.title} (${it.year})` : it.title}>
                <Artwork src={it.poster} width={72} alt={it.title} fallback={<span className={styles.noPoster}>{it.title}</span>} />
              </li>
            ))}
          </ul>
        )}
      </ListRow>
    </GroupedSection>
  )
}
