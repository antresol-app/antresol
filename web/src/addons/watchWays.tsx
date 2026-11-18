// The addons' watch ways (`itemActions` entries with `watchWay: true`) as
// ways of a «Смотреть ▾»: id `<addonId>:<label>`, the label resolved, an
// entry's `visible` and `useAvailable` applied. Each `useAvailable` is a
// hook: the core calls it in a component of its own per entry (a Host,
// drawn in `node`), and a way whose hook still asks (null) is not offered
// yet. A kid gets none. The SDK's useWatchWays (another addon's rows) draws
// its Hosts and sheets in the shell's AddonWaysOutlet.
import {
  Fragment,
  memo,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useState,
  useSyncExternalStore,
  type ReactElement,
  type ReactNode,
} from 'react'
import { errorText } from '../api/client'
import type { ItemDetail } from '../api/types'
import type { WatchWay } from '../components/WatchButton'
import { useLang, useT } from '../i18n'
import { fromTitle } from '../pages/titleDetail'
import { showNotice } from '../state/notice'
import { addonString, useRegistryVersion, type ItemAction, type WayTitle } from './registry'
import { AddonBoundary, useAddonSheet, useAddonSlot } from './slots'

type Answer = boolean | null
type UseAvailable = NonNullable<ItemAction['useAvailable']>
type HostProps = { addonId: string; answerKey: string; use: UseAvailable; target: WayTitle; onAnswer: (key: string, a: Answer) => void }

function Ask({ use, target, answerKey, onAnswer }: HostProps) {
  const said = use(target)
  // only a boolean is an answer; anything else still asks
  const a: Answer = said === true || said === false ? said : null
  useLayoutEffect(() => onAnswer(answerKey, a), [answerKey, a, onAnswer])
  return null
}

// one Host per entry: a throw hides its way only (answered false)
const AvailabilityHost = memo(function AvailabilityHost(props: HostProps) {
  return (
    <AddonBoundary addonId={props.addonId} fallback={null} onError={() => props.onAnswer(props.answerKey, false)}>
      <Ask {...props} />
    </AddonBoundary>
  )
})

// a number per hook function: a new registration's hook gets a Host of its own
const hookIds = new WeakMap<UseAvailable, number>()
let lastHookId = 0
function hookId(use: UseAvailable): number {
  let n = hookIds.get(use)
  if (n === undefined) hookIds.set(use, (n = ++lastHookId))
  return n
}

/** What a way said: text in a notice, or a failure. */
export type WaySaid = { text: string; failed: boolean }

export interface AddonWays {
  /** The ways offered now, in addon id order. */
  ways: WatchWay[]
  /** The availability Hosts and the element a way's run drew: draw it always, in one place. */
  node: ReactNode
  /** What the last run said (its text, or its failure). */
  said: WaySaid | null
  /** Ways whose `useAvailable` still asks: the «Смотреть ▾» menu may gain them. */
  pending: number
}

/** The profile's addon watch ways for `item` (none for a kid). `item` must be stable (memoised). */
export function useAddonWays(item: ItemDetail): AddonWays {
  const t = useT()
  const lang = useLang()
  const v = useRegistryVersion()
  const actions = useAddonSlot('itemActions')
  const [said, setSaid] = useState<WaySaid | null>(null)
  const sheet = useAddonSheet(() => setSaid({ text: t('addons.actionFailed'), failed: true }))
  const run = sheet.run
  const [answers, setAnswers] = useState<Record<string, Answer>>({})
  const onAnswer = useCallback((key: string, a: Answer) => setAnswers((m) => (m[key] === a ? m : { ...m, [key]: a })), [])
  const tmdbId = item.tmdbId ?? 0
  const kind = item.kind === 'movie' ? 'movie' : item.kind === 'series' ? 'series' : null
  const target = useMemo<WayTitle | null>(() => (kind ? { kind, tmdbId, itemId: item.id } : null), [kind, tmdbId, item.id])
  const { ways, hosts, pending } = useMemo(() => {
    void v
    const ways: WatchWay[] = []
    const hosts: ReactElement[] = []
    let pending = 0
    // an entry's place among its addon's entries (another addon coming or going keeps it)
    const seen = new Map<string, number>()
    for (const a of actions) {
      const n = seen.get(a.addonId) ?? 0
      seen.set(a.addonId, n + 1)
      if (!a.watchWay) continue
      try {
        if (a.visible && !a.visible(item)) continue
      } catch (e) {
        console.warn(`antresol: addon ${a.addonId} watch way failed`, e)
        continue
      }
      if (a.useAvailable !== undefined) {
        // a malformed hook, or a kind it is not asked about: not offered
        if (typeof a.useAvailable !== 'function' || !target) continue
        const hostKey = `${a.addonId}\n${n}\n${hookId(a.useAvailable)}`
        const answerKey = `${hostKey}\n${target.kind}:${target.tmdbId}:${target.itemId}`
        hosts.push(<AvailabilityHost key={hostKey} addonId={a.addonId} answerKey={answerKey} use={a.useAvailable} target={target} onAnswer={onAnswer} />)
        const answer = answers[answerKey]
        if (answer !== true) {
          if (answer !== false) pending++
          continue
        }
      }
      ways.push({
        id: `${a.addonId}:${a.label}`,
        label: addonString(a.addonId, lang, a.label),
        run: () => {
          setSaid(null)
          let answer: ReturnType<typeof a.run> | undefined
          try {
            answer = run(a.addonId, (close) => a.run(item, close))
          } catch (e) {
            console.warn(`antresol: addon ${a.addonId} watch way failed`, e)
            setSaid({ text: errorText(e, t('addons.actionFailed')), failed: true })
            return
          }
          void Promise.resolve(answer).then(
            (text) => typeof text === 'string' && text && setSaid({ text, failed: false }),
            (e: unknown) => {
              console.warn(`antresol: addon ${a.addonId} watch way failed`, e)
              setSaid({ text: errorText(e, t('addons.actionFailed')), failed: true })
            },
          )
        },
      })
    }
    return { ways, hosts, pending }
  }, [actions, item, lang, v, run, t, answers, target, onAnswer])
  // the same element until a Host or the sheet changes (useWatchWays draws it only then)
  const sheetNode = sheet.node
  const node = useMemo(
    () => (
      <>
        {hosts}
        {sheetNode}
      </>
    ),
    [hosts, sheetNode],
  )
  return { ways, node, said, pending }
}

// What the SDK's useWatchWays draws (its Hosts, the sheets its ways open),
// by caller: the shell's AddonWaysOutlet draws them.
const drawn = new Map<string, ReactNode>()
let drawnVersion = 0
const drawnListeners = new Set<() => void>()
function draw(id: string, node: ReactNode | null) {
  if (node === null) {
    if (!drawn.delete(id)) return
  } else drawn.set(id, node)
  drawnVersion++
  for (const l of drawnListeners) l()
}
const subscribeDrawn = (l: () => void) => {
  drawnListeners.add(l)
  return () => void drawnListeners.delete(l)
}
const drawnSnapshot = () => drawnVersion

/** Where the SDK's useWatchWays draws its availability Hosts and the sheets its ways open (the shell has one). */
export function AddonWaysOutlet() {
  useSyncExternalStore(subscribeDrawn, drawnSnapshot, drawnSnapshot)
  return (
    <>
      {[...drawn].map(([id, node]) => (
        <Fragment key={id}>{node}</Fragment>
      ))}
    </>
  )
}

/**
 * The SDK's: the `watchWay` entries of every addon the profile has, as ways
 * for the title `t` (a title outside the library: `item.id` 0), with their
 * `visible` and `useAvailable` applied; none for a kid. The calling addon's
 * own entries are among them (ids `<its id>:<label>`). What a way's run
 * draws is drawn by the app; what it says shows in the app's notice.
 */
export function useWatchWays(t: { kind: 'movie' | 'series'; tmdbId: number; title: string; poster?: string }): WatchWay[] {
  const { kind, tmdbId, title, poster } = t
  const item = useMemo(
    () => fromTitle({ tmdbId, kind, title, year: 0, poster: poster ?? '', backdrop: '', overview: '', inLibrary: false, libraryItemId: null, genres: [], seasons: [] }, false),
    [kind, tmdbId, title, poster],
  )
  const { ways, node, said } = useAddonWays(item)
  const id = useId()
  // drawn when what it draws changed, not on each render of the caller
  useLayoutEffect(() => draw(id, node), [id, node])
  useLayoutEffect(() => () => draw(id, null), [id])
  useEffect(() => {
    if (said) showNotice(said.text)
  }, [said])
  return ways
}
