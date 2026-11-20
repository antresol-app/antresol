// Where «ТВ» (/tv) was left on this device: the category (?f), the view
// (list or guide), the channel in the details pane and how far down the
// page was, so opening «ТВ» again comes back to it, as a television keeps
// its channel list. A per-device convenience: plain localStorage, which may
// be missing or throw (a private window), and then /tv opens at its top.

const KEY = 'vault.tvPlace'

export interface TvPlace {
  f?: string
  view?: 'list' | 'guide'
  previewId?: number
  scrollY?: number
}

export function loadTvPlace(): TvPlace {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(KEY) ?? 'null')
    if (!v || typeof v !== 'object') return {}
    const o = v as Record<string, unknown>
    const out: TvPlace = {}
    if (typeof o.f === 'string' && o.f) out.f = o.f
    if (o.view === 'list' || o.view === 'guide') out.view = o.view
    if (typeof o.previewId === 'number' && Number.isFinite(o.previewId)) out.previewId = o.previewId
    if (typeof o.scrollY === 'number' && Number.isFinite(o.scrollY) && o.scrollY > 0) out.scrollY = Math.round(o.scrollY)
    return out
  } catch {
    return {}
  }
}

/** Merges part of the place into what is kept. */
export function saveTvPlace(part: TvPlace): void {
  try {
    const next = { ...loadTvPlace(), ...part }
    for (const k of Object.keys(next) as (keyof TvPlace)[]) if (next[k] === undefined || next[k] === '') delete next[k]
    localStorage.setItem(KEY, JSON.stringify(next))
  } catch {
    // no storage: /tv opens at its top
  }
}
