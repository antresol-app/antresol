// Loads the web bundles of the signed-in profile's addons (GET /api/addons/web).
// A bundle registers itself (registerAddon) when it runs; one that fails or
// hangs is skipped with a warning, never taking the app down.
import { listenEvent, onEvents } from '../api/events'
import { request } from '../api/client'
import { contributionsOf, setActiveAddons, setAddonUser, setAddonWebFailure, setAddonsLoading, setAddonsSettled } from './registry'

type WebAddon = { id: string; entry: string; style?: string }
type Profile = { id: number; isAdmin: boolean; isKid?: boolean; addons?: boolean }

const TIMEOUT_MS = 10_000

/**
 * The addons' stylesheets (index.css of their CSS modules): one
 * <link rel="stylesheet" data-addon="<id>"> each, the URL versioned like the
 * entry. A stylesheet never holds up its script, and one that fails to load
 * only leaves the addon unstyled; the links of addons no longer listed go.
 */
function syncStyles(list: WebAddon[]) {
  const want = new Map(list.filter((a) => a.style).map((a) => [a.id, a.style!]))
  for (const link of document.head.querySelectorAll<HTMLLinkElement>('link[data-addon]')) {
    const id = link.dataset.addon ?? ''
    if (want.get(id) === link.getAttribute('href')) want.delete(id)
    else link.remove()
  }
  for (const [id, href] of want) {
    const link = document.createElement('link')
    link.rel = 'stylesheet'
    link.href = href
    link.dataset.addon = id
    link.onerror = () => console.warn(`antresol: addon ${id}: its stylesheet did not load`)
    document.head.append(link)
  }
}

/** How an entry is imported (tests stub it). */
export const importer = {
  load: (url: string): Promise<unknown> => import(/* @vite-ignore */ url),
}

let loadedFor: number | null = null
let pending: Promise<void> | null = null
// the profile the last load was for (reloadAddons loads for it again)
let current: Profile | null = null
/** The addons whose web part did not load this time (id → why), for «Состояние». */
const webFailed = new Map<string, string>()

/** The addons whose web part did not load (id → the error's words). */
export function addonWebFailures(): ReadonlyMap<string, string> {
  return webFailed
}

function withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`timed out after ${ms} ms`)), ms)
    p.then(
      (v) => {
        clearTimeout(timer)
        resolve(v)
      },
      (e: unknown) => {
        clearTimeout(timer)
        reject(e instanceof Error ? e : new Error(String(e)))
      },
    )
  })
}

async function load(user: Profile, fresh: boolean) {
  // the previous profile's addons leave the slots at once (a reload keeps them until the new list)
  if (fresh) setActiveAddons([])
  const admin = user.isAdmin && !user.isKid
  setAddonUser({ isAdmin: admin })
  let list: WebAddon[] = []
  // never for kids, and only on a server that runs addons; the server lists
  // what the profile may load (an adult: the addons with access "adults")
  if (!user.isKid && user.addons !== false) {
    try {
      list = await request<WebAddon[]>('GET', '/api/addons/web')
    } catch (e) {
      console.warn('antresol: addon list unavailable', e)
      list = []
    }
  }
  if (!Array.isArray(list)) list = []
  if (loadedFor !== user.id) return // another profile signed in meanwhile
  setActiveAddons(list.map((a) => a.id))
  syncStyles(list)
  if (list.length === 0) return
  // React, the router and the SDK for the bundles: fetched only when there is one to load
  try {
    const { installShared } = await import('./shared')
    installShared()
  } catch (e) {
    console.warn('antresol: addon runtime unavailable', e)
    return
  }
  await Promise.all(
    list.map(async (a) => {
      try {
        await withTimeout(importer.load(a.entry), TIMEOUT_MS)
        webFailed.delete(a.id)
      } catch (e) {
        console.warn(`antresol: addon ${a.id} did not load`, e)
        webFailed.set(a.id, e instanceof Error ? e.message : String(e))
        // said on its row of «Расширения», not only in the console
        setAddonWebFailure(a.id, e instanceof Error ? e.message : String(e))
        return
      }
      // a bundle that ran but never called register() contributes nothing either
      if (!contributionsOf(a.id)) {
        console.warn(`antresol: addon ${a.id}: its bundle did not register`)
        setAddonWebFailure(a.id, 'did not register')
        return
      }
      setAddonWebFailure(a.id, null)
      for (const name of contributionsOf(a.id)?.events ?? []) {
        listenEvent(`addon.${a.id}.${name}`)
        if (admin) listenEvent(`addon.${a.id}.admin.${name}`)
      }
    }),
  )
}

function start(user: Profile, fresh: boolean): Promise<void> {
  loadedFor = user.id
  current = user
  setAddonsLoading(true)
  const run = load(user, fresh).finally(() => {
    if (pending !== run) return
    setAddonsLoading(false)
    if (loadedFor === user.id) setAddonsSettled(user.id)
  })
  pending = run
  return run
}

// An addon's status changed on the server (addons.changed), or the event
// stream connected again after the server was away (a restart: what it said
// meanwhile is lost): the list is asked again. GET /api/addons/web lists the
// running addons only, so one still starting when the tab loaded (the page
// reloads itself as soon as an updated server answers, before a slower addon
// is up) or one back after a crash joins the slots here, not after a manual
// reload. Several changes in a row make one reload.
let unlisten: (() => void) | null = null
let soon: ReturnType<typeof setTimeout> | undefined
const RELOAD_DELAY_MS = 300

function follow() {
  if (unlisten) return
  unlisten = onEvents(() => {
    clearTimeout(soon)
    soon = setTimeout(() => void reloadAddons(), RELOAD_DELAY_MS)
  }, ['addons.changed', 'open'])
}

/** Loads the addons of `user`; once per profile (a second call for the same one waits for the first). */
export function initAddons(user: Profile): Promise<void> {
  if (loadedFor === user.id && pending) return pending
  if (!user.isKid && user.addons !== false) follow()
  return start(user, true)
}

/**
 * Loads the signed-in profile's addons again (after one was installed,
 * switched on or off, updated or removed): new ones join the slots, gone
 * ones leave them, without a page reload. An updated addon's entry is a
 * new URL (?v=<version>), so its new bundle runs and registers anew,
 * replacing the old contributions; an unchanged one is not run twice.
 */
export function reloadAddons(): Promise<void> {
  if (!current) return Promise.resolve()
  const user = current
  // after the load in flight, so the two never race for the slots
  const run = (pending ?? Promise.resolve()).catch(() => {}).then(() => (loadedFor === user.id ? start(user, false) : undefined))
  return run
}

export function resetLoaderForTests() {
  unlisten?.()
  unlisten = null
  clearTimeout(soon)
  for (const link of document.head.querySelectorAll('link[data-addon]')) link.remove()
  loadedFor = null
  pending = null
  current = null
  webFailed.clear()
}
