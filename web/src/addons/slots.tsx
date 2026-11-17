// What the core screens use to show addon contributions: the slot's entries
// for this profile (the addons the server listed for it, `admin: true`
// entries for admins only; kids get none) and the boundary every addon-made element is drawn inside, so an
// addon that throws leaves a notice in its place instead of a broken page.
import {
  Component,
  isValidElement,
  memo,
  useCallback,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ComponentType,
  type ReactElement,
  type ReactNode,
} from 'react'
import { matchPath } from 'react-router'
import type { EpisodeCtx, EpisodePlay } from '../components/episodes/episodeTypes'
import { useLang, useT } from '../i18n'
import { useSession } from '../state/session'
import { Notice } from '../ui/Notice'
import {
  addonString,
  useAddonsSettledFor,
  useContributions,
  useRegistryVersion,
  type Contribution,
  type EpisodeAction,
  type ProfileMenuEntry,
  type Slot,
} from './registry'

const NONE: never[] = []

/** The entries of slot `k` the signed-in profile sees (none for a kid). */
export function useAddonSlot<K extends Slot>(k: K): Contribution<K>[] {
  const { user } = useSession()
  const all = useContributions(k)
  return user && !user.isKid ? all : NONE
}

/**
 * Whether the profile can add titles from outside the library: an addon's
 * search block is there for it (never for a kid; an `admin: true` block
 * for admins only).
 */
export function useCanAdd(): boolean {
  return useAddonSlot('searchBlocks').length > 0
}

/**
 * useCanAdd, with "not known yet" (undefined) while a server with addons
 * has not finished loading the profile's: what a profile's own choices
 * depend on (its «Моя медиатека», the texts that name adding) is held
 * until then instead of reading as «no» first and flipping.
 */
export function useCanAddState(): boolean | undefined {
  const { user } = useSession()
  const yes = useCanAdd()
  const settled = useAddonsSettledFor()
  if (yes) return true
  if (!user || user.isKid || user.addons !== true) return false
  return settled === user.id ? false : undefined
}

const trimSlashes = (p: string) => p.replace(/^\/+|\/+$/g, '')

/** A URL path as the router matches the app's own routes: each segment decoded, an encoded slash kept inside its segment (it reads as `/` in a value). */
function decodePath(p: string): string {
  try {
    return p
      .split('/')
      .map((v) => decodeURIComponent(v).replace(/\//g, '%2F'))
      .join('/')
  } catch {
    return p
  }
}

/**
 * The route of addon `addonId` that `rest` (the path after /addons/<id>/,
 * as the location has it: percent-encoded) names, with the values of its
 * `:name` segments, decoded; null: none. A plain path matches as it is (one
 * leading or trailing slash aside; case counts).
 */
export function matchAddonRoute(
  routes: readonly Contribution<'routes'>[],
  addonId: string,
  rest: string,
): { route: Contribution<'routes'>; params: Record<string, string> } | null {
  const want = '/' + decodePath(trimSlashes(rest))
  for (const route of routes) {
    if (route.addonId !== addonId) continue
    const m = matchPath({ path: '/' + trimSlashes(route.path), caseSensitive: true, end: true }, want)
    if (!m) continue
    const params: Record<string, string> = {}
    for (const [k, v] of Object.entries(m.params)) if (v !== undefined) params[k] = v
    return { route, params }
  }
  return null
}

/**
 * The path after /addons/<addonId>/ in `pathname` (empty for the addon's
 * root). The prefix is as case-insensitive as the app's route to it
 * (/Addons/<id>/… is the same page); the rest keeps its case.
 */
export function addonRest(pathname: string, addonId: string): string {
  const prefix = `/addons/${addonId}`
  if (pathname.slice(0, prefix.length).toLowerCase() !== prefix.toLowerCase()) return ''
  if (pathname.length > prefix.length && pathname[prefix.length] !== '/') return ''
  return pathname.slice(prefix.length).replace(/^\/+/, '')
}

/** The profile's route of addon `addonId` at `rest` (none for a kid); see matchAddonRoute. */
export function useAddonRoute(addonId: string, rest: string) {
  const routes = useAddonSlot('routes')
  return useMemo(() => matchAddonRoute(routes, addonId, rest), [routes, addonId, rest])
}

/** Where an addon's `to` leads: an app path (starting with `/`) as it is, else under /addons/<id>/. */
export function resolveAddonPath(addonId: string, to: string): string {
  return to.startsWith('/') ? to : `/addons/${addonId}/${to}`
}

/**
 * Resolves the labels and titles of addon `addonId`'s contributions: a key
 * of its `strings` in the interface language (`{name}` takes `vars.name`);
 * a key it does not have shows as itself. Follows a language switch and a
 * new registration.
 */
export function useAddonLabel(addonId: string): (key: string, vars?: Record<string, string | number>) => string {
  const lang = useLang()
  const v = useRegistryVersion()
  return useMemo(() => {
    void v
    return (key, vars) => addonString(addonId, lang, key, vars)
  }, [addonId, lang, v])
}

function SlotFailed({ as }: { as?: 'div' | 'li' }) {
  const t = useT()
  return (
    <Notice as={as} tone="destructive">
      {t('addons.slotFailed')}
    </Notice>
  )
}

interface Props {
  /** The addon the element is of (named in the console when it throws). */
  addonId: string
  children: ReactNode
  /** What to show instead (default: the notice). */
  fallback?: ReactNode
  /** The notice as a list item (inside a list). */
  as?: 'div' | 'li'
  /** Called once when the element throws. */
  onError?: () => void
}

/** Draws an addon's element; when it throws, the notice (or `fallback`) takes its place. */
export class AddonBoundary extends Component<Props, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() {
    return { failed: true }
  }
  componentDidCatch(error: unknown) {
    console.warn(`antresol: addon ${this.props.addonId} failed to render`, error)
    this.props.onError?.()
  }
  render() {
    if (!this.state.failed) return this.props.children
    return this.props.fallback !== undefined ? this.props.fallback : <SlotFailed as={this.props.as} />
  }
}

/**
 * The core's side of a `run(target, close)` that may answer an element (an
 * item action, a watch way, a player track action): `run(addonId, call)`
 * calls `call(close)`; an element it returns is drawn in `node` (inside the
 * addon's boundary) until that `close()`; anything else is handed back.
 * One element at a time: a new one takes the place of the last, and a stale
 * `close()` closes nothing; `closeAll()` closes it from the core's side (the
 * target changed). `onFail` is told when the element throws (it is then gone).
 */
export function useAddonSheet(onFail?: (addonId: string) => void): {
  node: ReactNode
  closeAll: () => void
  run: <R>(addonId: string, call: (close: () => void) => R) => Exclude<R, ReactElement> | undefined
} {
  const [open, setOpen] = useState<{ addonId: string; el: ReactElement; token: number } | null>(null)
  const seq = useRef(0)
  const fail = useRef(onFail)
  useLayoutEffect(() => {
    fail.current = onFail
  })
  const run = useCallback(<R,>(addonId: string, call: (close: () => void) => R): Exclude<R, ReactElement> | undefined => {
    const token = ++seq.current
    const close = () => setOpen((o) => (o?.token === token ? null : o))
    const said = call(close)
    if (isValidElement(said)) {
      setOpen({ addonId, el: said, token })
      return undefined
    }
    return said as Exclude<R, ReactElement>
  }, [])
  const node = open ? (
    <AddonBoundary
      key={open.token}
      addonId={open.addonId}
      fallback={null}
      onError={() => {
        setOpen((o) => (o?.token === open.token ? null : o))
        fail.current?.(open.addonId)
      }}
    >
      {open.el}
    </AddonBoundary>
  ) : null
  const closeAll = useCallback(() => setOpen(null), [])
  return { node, run, closeAll }
}

/** One entry of `episodeActions`, ready to draw: its kinds and the component that calls its `use`. */
export type EpisodeActionHost = {
  addonId: string
  /** The entry's own kinds (the core's dropped). */
  kinds: string[]
  /** Its kinds' labels (strings keys) and order, as the entry says them. */
  labels: Record<string, string>
  order: Record<string, number>
  /** A React key of its own: the addon, the entry's place among the addon's entries, its kinds. */
  key: string
  /**
   * Calls the entry's `use(ctx)` and draws its answer's `node`; `onPlay(kind,
   * answer)` for each of the kinds after a render whose answer changed, and
   * `(kind, null)` when it goes (or throws). Keep `ctx` and `onPlay` stable
   * (memoised): a new one re-runs it.
   */
  Host: ComponentType<{ ctx: EpisodeCtx; onPlay: (kind: string, p: EpisodePlay | null) => void }>
}

type HostProps = { ctx: EpisodeCtx; onPlay: (kind: string, p: EpisodePlay | null) => void }

// one Host per entry (by its hook and its key: addon, place, kinds), so a
// registration elsewhere re-uses it and its hook keeps its state
const hosts = new WeakMap<EpisodeAction['use'], Map<string, ComponentType<HostProps>>>()

function hostOf(key: string, addonId: string, kinds: string[], use: EpisodeAction['use']): ComponentType<HostProps> {
  let byKey = hosts.get(use)
  if (!byKey) hosts.set(use, (byKey = new Map()))
  let host = byKey.get(key)
  if (host) return host
  function Use({ ctx, onPlay }: HostProps) {
    const p = use(ctx)
    const last = useRef(onPlay)
    useLayoutEffect(() => {
      last.current = onPlay
      for (const k of kinds) onPlay(k, p)
    }, [p, onPlay])
    useLayoutEffect(() => () => kinds.forEach((k) => last.current(k, null)), [])
    return <>{p?.node}</>
  }
  host = memo(function EpisodeActionHost(props: HostProps) {
    return (
      <AddonBoundary addonId={addonId} fallback={null}>
        <Use {...props} />
      </AddonBoundary>
    )
  })
  byKey.set(key, host)
  return host
}

/**
 * The profile's `episodeActions` entries, each with the component that
 * calls its hook (none for a kid). A guide draws every Host and lifts what
 * `onPlay` says into a map by kind; an entry registered later adds a Host
 * of its own, so no hook order changes.
 */
export function useEpisodeActions(): EpisodeActionHost[] {
  const slot = useAddonSlot('episodeActions')
  return useMemo(() => {
    const seen = new Map<string, number>()
    const out: EpisodeActionHost[] = []
    for (const a of slot) {
      // the entry's place among its addon's own entries (another addon coming or going keeps it)
      const n = seen.get(a.addonId) ?? 0
      seen.set(a.addonId, n + 1)
      const kinds = (a.kinds ?? []).filter((k) => {
        if (!CORE_KINDS.has(k)) return true
        warnOnce(a.use, `antresol: addon ${a.addonId}: episodeActions kind "${k}" dropped, the core plays it`)
        return false
      })
      if (kinds.length === 0) continue
      // a malformed map (not an object) names nothing
      const pick = <V,>(m: Record<string, V> | undefined) => (m && typeof m === 'object' ? Object.fromEntries(kinds.filter((k) => Object.hasOwn(m, k)).map((k) => [k, m[k]!])) : {})
      // the library always ranks first: an order must be above its 0
      const order = Object.fromEntries(
        Object.entries(pick(a.order)).filter(([k, v]) => {
          if (typeof v === 'number' && Number.isFinite(v) && v > 0) return true
          warnOnce(a.use, `antresol: addon ${a.addonId}: episodeActions order of "${k}" dropped, it must be a number above 0 (the library's)`)
          return false
        }),
      ) as Record<string, number>
      const key = `${a.addonId}\n${n}\n${kinds.join('\n')}`
      out.push({ addonId: a.addonId, kinds, labels: pick(a.labels), order, key, Host: hostOf(key, a.addonId, kinds, a.use) })
    }
    return out
  }, [slot])
}

// what each entry (by its hook) was warned about: once, not on every recompute
const warned = new WeakMap<EpisodeAction['use'], Set<string>>()
function warnOnce(use: EpisodeAction['use'], line: string) {
  let seen = warned.get(use)
  if (!seen) warned.set(use, (seen = new Set()))
  if (seen.has(line)) return
  seen.add(line)
  console.warn(line)
}

/** The source kinds the core plays itself; an addon's entry never takes them. */
const CORE_KINDS = new Set(['library', 'online'])

/** How the episode guide sorts and names source kinds (useSourceKinds). */
export type SourceKinds = {
  /** Each kind's place (pickSource's `order`): the core's (library 0, online 20), then the addons'. */
  order: Record<string, number>
  /** A kind's name: the core's, its addon's label, else the kind itself. */
  label: (kind: string) => string
}

/**
 * The core's kinds (library, online) and the profile's addons' kinds, as
 * their episodeActions entries order and name them (none for a kid); the
 * first entry that names a kind wins.
 */
export function useSourceKinds(): SourceKinds {
  const entries = useEpisodeActions()
  const t = useT()
  const lang = useLang()
  return useMemo(() => {
    const order: Record<string, number> = { library: 0, online: 20 }
    const labelOf = new Map<string, { addonId: string; key: string }>()
    for (const e of entries) {
      for (const k of e.kinds) {
        if (!(k in order) && k in e.order) order[k] = e.order[k]!
        const key = e.labels[k]
        if (!labelOf.has(k) && typeof key === 'string') labelOf.set(k, { addonId: e.addonId, key })
      }
    }
    const label = (kind: string) => {
      if (kind === 'library') return t('episodes.kind.library')
      if (kind === 'online') return t('episodes.kind.online')
      const l = labelOf.get(kind)
      return l ? addonString(l.addonId, lang, l.key) : kind
    }
    return { order, label }
  }, [entries, t, lang])
}

/** The profile menu's addon entries for the signed-in profile (none for a kid): `visible(user)` hides one, a throwing one too; `to` resolved. */
export function useProfileMenu(): (Contribution<'profileMenu'> & { to: string })[] {
  const { user } = useSession()
  const slot = useAddonSlot('profileMenu')
  return useMemo(() => {
    if (!user) return []
    const shown = (e: ProfileMenuEntry & { addonId: string }) => {
      if (!e.visible) return true
      try {
        return e.visible(user)
      } catch (err) {
        console.warn(`antresol: addon ${e.addonId} profile menu entry failed`, err)
        return false
      }
    }
    return slot.filter(shown).map((e) => ({ ...e, to: resolveAddonPath(e.addonId, e.to) }))
  }, [slot, user])
}
