// What addons contribute to the app (routes, «Управление» tiles, item actions, …),
// and which of them the signed-in profile has. A small external store so the
// slots re-render when an addon registers.
import { useMemo, useSyncExternalStore, type ReactElement } from 'react'
import type { IconName } from '../components/Icon'
import type { SettingsGlyph, SettingsTint } from '../ui/SettingsIcon'
import type { CardKind, ImportJob, ImportService, ItemDetail, KidRequest, PlayerInput, TitleInfo, User } from '../api/types'
import type { EpisodeCtx, EpisodePlay } from '../components/episodes/episodeTypes'
import type { SearchScope } from '../app/sections'
import type { Lang } from '../i18n'

/**
 * `admin: true` on any entry: shown to admins only (an addon with access
 * "adults" reaches other adult profiles too). The server keeps the
 * addon's x/admin/* for admins whatever the app shows.
 */
type AdminOnly = { admin?: boolean }
/*
 * Every `label`/`title` (and `migrateSources`' `name`) below is a key of the addon's `strings`, resolved
 * when the core draws it, in the interface language (a key that is not
 * there shows as itself).
 */
/** What an addon route's element gets: the values of its path's `:name` segments. */
export type AddonRouteProps = { params: Readonly<Record<string, string>> }
/**
 * A page of the addon; it mounts at /addons/<id>/<path>. `path` may hold
 * `:name` segments (`play/:kind/:id`); the values reach the element as its
 * `params` prop and as `useParams()` (the app's react-router, shared).
 * `fullWindow: true`: drawn over the whole window like the core's
 * players, outside the shell (no navigation, no mini player); never for
 * kids, as every addon route.
 */
export type AddonRoute = { path: string; element: (props: AddonRouteProps) => ReactElement; title: string; fullWindow?: true } & AdminOnly
/** The title a watch way's `useAvailable` is asked about (`itemId` 0: not in the library). */
export type WayTitle = { kind: 'movie' | 'series'; tmdbId: number; itemId: number }
/**
 * The groups of «Управление» a tile can stand in (Manage.tsx draws them in
 * this order): `family` (profiles, requests), `library` (what is in it, how it
 * comes in), `playback`, `downloads` (downloads and sources: where titles come
 * from), `server` (the server itself). A tile without one goes under
 * «Расширения», after the addons' groups.
 */
export type ManageGroup = 'family' | 'library' | 'playback' | 'downloads' | 'server'
/**
 * A row of «Управление»: `icon` is a glyph of the settings tiles
 * (ui/SettingsIcon, `SettingsGlyph`), drawn white on `tint`, a palette
 * colour (`SettingsTint`; gray without one), in `group`. A line icon of the
 * app (`IconName`, the contract before the tile glyphs) still draws: as the
 * tile glyph of the same meaning, else the puzzle piece.
 */
export type ManageTile = { label: string; icon: SettingsGlyph | IconName; tint?: SettingsTint; group?: ManageGroup; to: string } & AdminOnly
export type ItemAction = AdminOnly & {
  label: string
  icon: IconName
  visible?: (item: ItemDetail) => boolean
  /**
   * Offered as a way of the page's «Смотреть ▾» (beside the other addons'
   * ways, way id `<addonId>:<label>`) instead of as a round button.
   */
  watchWay?: true
  /**
   * A hook (a watch way's): whether the way is offered for the title,
   * `true` offered, `false` not, `null` still asking (not offered yet; the
   * «Смотреть ▾» menu gains the way when it answers `true`, nothing else
   * moves). The core calls it in a component of its own per entry, with
   * `itemId` 0 for a title outside the library; one that throws hides only
   * its way. Only for a movie or a series: on another kind the way is not
   * offered.
   */
  useAvailable?: (t: WayTitle) => boolean | null
  /**
   * Text it returns (non-empty) shows in a notice under the item's action
   * row; an element it returns is drawn by the core (a sheet, `ui.Sheet`)
   * until it calls `close`.
   */
  run: (item: ItemDetail, close: () => void) => void | string | ReactElement | Promise<void | string>
}
/**
 * A panel under the action row of an item's page and of a title's page
 * (there `item.id` is 0 and `title` the title). With `inRow` the element is
 * not drawn under the row but among its round buttons, after the addons'
 * `itemActions` (a toggle that keeps its own state, «Следить»): it draws one
 * `ui.IconButton` with `tile` (a phone shows it as one of the row's tiles).
 */
export type ItemPanel = AdminOnly & { element: (props: { item: ItemDetail; title?: TitleInfo }) => ReactElement | null; inRow?: true }
/**
 * What the episode guide does with the episodes' sources of `kinds` (kinds
 * the addon's scanners declare; the core's own `library` is dropped). `use`
 * is a hook: the core calls it in a component of its own per entry, once
 * per guide. `labels`: a kind's name, a key of the addon's strings (none:
 * the kind as it is). `order`: where a kind sorts among the sources, on a
 * scale where the core's `library` is 0 (none: last).
 */
export type EpisodeAction = AdminOnly & {
  kinds: string[]
  labels?: Record<string, string>
  order?: Record<string, number>
  use: (ctx: EpisodeCtx) => EpisodePlay | null
}
/** What the player plays, for a track action: a movie, or the series of an episode. */
export type TrackActionItem = { id: number; title: string; kind: CardKind }
/** A row of the player's track menu (never during online playback); an element `run` returns is drawn over the player until `close`. */
export type PlayerTrackAction = AdminOnly & {
  label: string
  icon?: IconName
  visible?: (item: TrackActionItem) => boolean
  run: (item: TrackActionItem, close: () => void) => void | ReactElement
}
/** An entry of the profile menu; `to` as NavBadge's. */
export type ProfileMenuEntry = AdminOnly & { label: string; icon: IconName; to: string; visible?: (user: User) => boolean }
/**
 * A note the player shows under its spinner while one of the addon's inputs
 * (/play/input/<id>) loads or rebuffers — in place of «Загружается…». Only
 * the notes of the addon that registered the input are shown.
 */
export type PlayerNote = AdminOnly & {
  element: (props: { input: PlayerInput }) => ReactElement | null
  /** Buttons the player shows at its top while the input plays (its `topActions`). */
  actions?: (props: { input: PlayerInput }) => ReactElement | null
  /**
   * A hook the core calls (in a component of its own per note) while the
   * input plays: how far the source has got (its `sourceProgress`); the
   * player keeps waiting while it grows.
   */
  useProgress?: (input: PlayerInput) => number
}
/**
 * A count beside the app's navigation (hidden at 0, `99+` above 99).
 * `to`: an app path starting with `/`, else relative to /addons/<id>/;
 * `label`: a strings key taking `{count}`.
 */
export type NavBadge = AdminOnly & { to: string; icon: IconName; label: string; useCount: () => number }
/** What «Поиск» shows under the library's results for the query (`nothing`: the library has none). */
export type SearchBlock = AdminOnly & {
  element: (props: { q: string; scope: SearchScope; text: string; nothing: boolean }) => ReactElement | null
}
/** A button on an empty library page of `scope`; `to` as NavBadge's. */
export type EmptyStateAction = AdminOnly & {
  scope: 'home' | 'video' | 'music' | 'book' | 'audiobook' | 'comic'
  label: string
  icon?: IconName
  to: string
}
/** The sheet «Одобрить» opens for a kid's request: the first entry whose `visible` says yes (none: approved at once). */
export type RequestAction = AdminOnly & {
  visible?: (request: KidRequest) => boolean
  element: (props: { request: KidRequest; onClose: () => void; onApproved: () => void }) => ReactElement
}
/** A block of «Управление → Статистика». */
export type StatsBlock = AdminOnly & { element: () => ReactElement | null }
/**
 * A card of «Управление → Переезд» that opens the addon's own migration
 * page: `name` is a strings key; `to` is an app path starting with `/`,
 * else relative to /addons/<id>/; `summary()` is drawn under the name
 * inside the card's link, so text only (a count of what waits, say).
 */
export type MigrateSourceEntry = AdminOnly & {
  id: string
  name: string
  icon: IconName
  to: string
  summary?: () => ReactElement | null
}
/** What «Импорт медиатеки» hands an import source (in place of its file import). */
export type ImportSourcesProps = {
  /** The services GET /api/import/services lists (never empty here). */
  services: ImportService[]
  /** Upload these files as the core's file import does. */
  onFiles: (files: File[]) => void
  /** An upload is under way. */
  uploading: boolean
  /** A job the source started (a sign-in's reading job): the page shows it. */
  onJob: (job: ImportJob) => void
  /** The source starts something: the page clears its problem and notice. */
  onStart: () => void
  /** A problem to say over the page. */
  onProblem: (text: string) => void
}
/**
 * The way in of «Импорт медиатеки» when the server lists import services:
 * the first entry is drawn in place of the file import; one that throws
 * leaves the file import.
 */
export type ImportSource = AdminOnly & { element: (props: ImportSourcesProps) => ReactElement | null }
export type AddonContributions = {
  routes?: AddonRoute[]
  manageTiles?: ManageTile[]
  itemActions?: ItemAction[]
  playerNotes?: PlayerNote[]
  navBadges?: NavBadge[]
  searchBlocks?: SearchBlock[]
  emptyStateActions?: EmptyStateAction[]
  requestActions?: RequestAction[]
  statsBlocks?: StatsBlock[]
  itemPanels?: ItemPanel[]
  episodeActions?: EpisodeAction[]
  playerTrackActions?: PlayerTrackAction[]
  profileMenu?: ProfileMenuEntry[]
  migrateSources?: MigrateSourceEntry[]
  importSources?: ImportSource[]
  /** Short event names: `progress` arrives as addon.<id>.progress (and addon.<id>.admin.progress for admins). */
  events?: string[]
  strings?: { ru: Record<string, string>; en: Record<string, string> }
}
export type Slot =
  | 'routes'
  | 'manageTiles'
  | 'itemActions'
  | 'playerNotes'
  | 'navBadges'
  | 'searchBlocks'
  | 'emptyStateActions'
  | 'requestActions'
  | 'statsBlocks'
  | 'itemPanels'
  | 'episodeActions'
  | 'playerTrackActions'
  | 'profileMenu'
  | 'migrateSources'
  | 'importSources'
type SlotItem<K extends Slot> = NonNullable<AddonContributions[K]>[number]
/** A slot entry with the addon it came from. */
export type Contribution<K extends Slot> = SlotItem<K> & { addonId: string }

// every registration this tab has seen: a module runs once, so a profile
// switch back must find it here rather than in a second import
const registered = new Map<string, AddonContributions>()
// the addons the server lists for the signed-in profile; null = no list yet
let active: Set<string> | null = null
// whether the signed-in profile is an admin (the loader says; admin: true entries wait for it)
let isAdmin = false
// the loader is fetching the list or importing bundles (an addon page waits for it)
let loading = false
// the profile whose first load finished (its can-add answer is known from then on)
let settledFor: number | null = null
let version = 0
const listeners = new Set<() => void>()

function changed() {
  version++
  for (const l of listeners) l()
}

const isActive = (id: string) => active === null || active.has(id)

/** /addons/<id>/ui/… is the addon's bundle on the server: no page of it may live there. */
const underUI = (path: string) => {
  const p = path.replace(/^\/+/, '')
  return p === 'ui' || p.startsWith('ui/')
}

// The path patterns of an addon's fullWindow routes, kept on this device:
// a cold load of such a URL draws the player's black fallback (not the
// shell) until the bundle registers. Storage may be missing or refuse.
const FULL_WINDOW_KEY = 'antresol.addonFullWindow.'

/** The fullWindow route patterns addon `id` registered last time on this device ([] when none or no storage). */
export function rememberedFullWindow(id: string): string[] {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(FULL_WINDOW_KEY + id) ?? '[]')
    return Array.isArray(v) ? v.filter((p): p is string => typeof p === 'string') : []
  } catch {
    return []
  }
}

function rememberFullWindow(id: string, routes: AddonRoute[] | undefined) {
  const paths = (routes ?? []).filter((r) => r.fullWindow).map((r) => r.path)
  try {
    if (paths.length) localStorage.setItem(FULL_WINDOW_KEY + id, JSON.stringify(paths))
    else localStorage.removeItem(FULL_WINDOW_KEY + id)
  } catch {
    // no storage: a cold load shows the shell first, as for an addon never seen
  }
}

// The addons whose web part the loader could not run this time, with why
// («Расширения» says it on the addon's row: an addon that runs on the server
// but adds nothing to the app is not silent).
const webFailures = new Map<string, string>()

/** The loader's verdict on addon `id`'s web part: why it failed, or null (it ran). */
export function setAddonWebFailure(id: string, why: string | null) {
  if ((webFailures.get(id) ?? null) === why) return
  if (why === null) webFailures.delete(id)
  else webFailures.set(id, why)
  changed()
}

/** Why addon `id`'s web part did not run in this tab (undefined: it ran, or was not tried). */
export function useAddonWebFailure(id: string): string | undefined {
  useSyncExternalStore(subscribe, snapshot, snapshot)
  return webFailures.get(id)
}

/** Called by an addon's bundle (as soon as it loads): its contributions; a second call replaces the first. */
export function registerAddon(id: string, c: AddonContributions): void {
  const routes = c.routes?.filter((r) => {
    if (!underUI(r.path)) return true
    console.warn(`antresol: addon ${id}: route "${r.path}" dropped, /addons/${id}/ui/ holds the bundle's files`)
    return false
  })
  registered.set(id, routes ? { ...c, routes } : c)
  rememberFullWindow(id, routes)
  changed()
}

/** The addons of the signed-in profile (from /api/addons/web); the others stay out of the slots. */
export function setActiveAddons(ids: readonly string[]) {
  active = new Set(ids)
  // an addon no longer listed is not tried: its old verdict goes
  for (const id of webFailures.keys()) if (!active.has(id)) webFailures.delete(id)
  changed()
}

/** Who is signed in (from the loader): `admin: true` entries show to admins only. */
export function setAddonUser(u: { isAdmin: boolean }) {
  if (isAdmin === u.isAdmin) return
  isAdmin = u.isAdmin
  changed()
}

/** The loader is at work: an addon's page that is not there yet may still come. */
export function setAddonsLoading(on: boolean) {
  if (loading === on) return
  loading = on
  changed()
}

/** The loader finished a load for profile `id` (null: none yet). */
export function setAddonsSettled(id: number | null) {
  if (settledFor === id) return
  settledFor = id
  changed()
}

/** The profile whose addons are known (re-renders when that changes). */
export function useAddonsSettledFor(): number | null {
  useSyncExternalStore(subscribe, snapshot, snapshot)
  return settledFor
}

/** Whether the loader is at work (re-renders when that changes). */
export function useAddonsLoading(): boolean {
  useSyncExternalStore(subscribe, snapshot, snapshot)
  return loading
}

/** The contributions of addon `id`, if it registered and the profile has it. */
export function contributionsOf(id: string): AddonContributions | undefined {
  return isActive(id) ? registered.get(id) : undefined
}

/** Addon `id`'s string `key` in `lang` (`{name}` takes `vars.name`); a key it does not have is itself. */
export function addonString(id: string, lang: Lang, key: string, vars?: Record<string, string | number>): string {
  const s = contributionsOf(id)?.strings?.[lang]?.[key] ?? key
  return vars ? s.replace(/\{(\w+)\}/g, (m, name: string) => (name in vars ? String(vars[name]) : m)) : s
}

/** Re-renders when an addon registers (or the active set changes); the store's version. */
export function useRegistryVersion(): number {
  return useSyncExternalStore(subscribe, snapshot, snapshot)
}

const subscribe = (fn: () => void) => {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}
const snapshot = () => version

/** Every entry of slot `k` across the profile's addons, in addon id order (`admin: true` ones for admins only); re-renders on registration. */
export function useContributions<K extends Slot>(k: K): Contribution<K>[] {
  const v = useSyncExternalStore(subscribe, snapshot, snapshot)
  return useMemo(() => {
    void v
    const ids = [...registered.keys()].filter(isActive).sort()
    return ids.flatMap((id) =>
      (registered.get(id)?.[k] ?? []).filter((item) => isAdmin || !item.admin).map((item) => ({ ...item, addonId: id }) as Contribution<K>),
    )
  }, [k, v])
}

export function resetAddonsForTests() {
  registered.clear()
  webFailures.clear()
  active = null
  isAdmin = false
  loading = false
  settledFor = null
  changed()
}
