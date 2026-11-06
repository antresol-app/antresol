// The interface language: what the person picked in Settings («Язык
// интерфейса», per browser), else the browser's preferred languages by the
// shared rule (the first ru*/en* wins; none → en). One store for the tab;
// components re-render through useT()/useLang().
//
// A catalog is a chunk of its own, loaded on demand (loadCatalog): a page
// downloads only the language it shows. main.tsx renders once the active one
// is in (the boot script has already asked for it, see boot/early.ts).
import { useMemo, useSyncExternalStore } from 'react'
import type { ru } from './ru'
import type { Entry, Params, Plural } from './types'

export type Lang = 'ru' | 'en'
export type LangPref = 'system' | Lang
export type Key = keyof typeof ru & string
export type TFunction = (key: Key, params?: Params) => string

export const LANGS: readonly Lang[] = ['ru', 'en']
export const STORAGE_KEY = 'vault.uiLanguage'

type Catalog = Partial<Record<Key, Entry>>
const catalogs: Partial<Record<Lang, Catalog>> = {}
const loading = new Map<Lang, Promise<void>>()
/** How each catalog is fetched (exported for tests, which stub a failure). */
export const catalogImporters: Record<Lang, () => Promise<Catalog>> = {
  ru: () => import('./ru').then((m) => m.ru),
  en: () => import('./en').then((m) => m.en),
}

/** Loads `l`'s catalog (once); resolves when translate() has it. A failure is forgotten: the next call asks again. */
export function loadCatalog(l: Lang): Promise<void> {
  if (catalogs[l]) return Promise.resolve()
  let p = loading.get(l)
  if (!p) {
    p = catalogImporters[l]().then(
      (c) => {
        catalogs[l] = c
      },
      (e: unknown) => {
        loading.delete(l)
        throw e
      },
    )
    loading.set(l, p)
  }
  return p
}

/** The first frame's catalog: asked twice before giving up (false: there are no strings to show). */
export async function loadCatalogAtBoot(l: Lang): Promise<boolean> {
  for (let i = 0; i < 2; i++) {
    try {
      await loadCatalog(l)
      return true
    } catch {
      /* once more */
    }
  }
  return false
}

/** Tests: forget a loaded catalog. */
export function forgetCatalogForTests(l: Lang) {
  delete catalogs[l]
  loading.delete(l)
}

/** The contract's rule: walk the preferred languages; the first ru-… or en-… wins; none → en. */
export function resolveLanguage(preferred: readonly string[] | undefined | null): Lang {
  for (const tag of preferred ?? []) {
    const primary = tag.trim().toLowerCase().split(/[-_]/)[0]
    if (primary === 'ru' || primary === 'en') return primary
  }
  return 'en'
}

function browserLanguages(): readonly string[] {
  if (typeof navigator === 'undefined') return []
  if (navigator.languages?.length) return navigator.languages
  return navigator.language ? [navigator.language] : []
}

function readPref(): LangPref {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (v === 'ru' || v === 'en' || v === 'system') return v
  } catch {
    /* storage blocked: follow the system */
  }
  return 'system'
}

let pref: LangPref = readPref()
let lang: Lang = pref === 'system' ? resolveLanguage(browserLanguages()) : pref
const listeners = new Set<() => void>()

function applyDocumentLang() {
  if (typeof document !== 'undefined') document.documentElement.lang = lang
}
applyDocumentLang()

const notify = () => {
  for (const l of [...listeners]) l()
}

function recompute() {
  const next = pref === 'system' ? resolveLanguage(browserLanguages()) : pref
  if (next === lang) return
  lang = next
  applyDocumentLang()
  notify()
  // until it is in, the strings stay in the language already loaded
  // a failure leaves the strings in the language already loaded; the next change asks again
  if (!catalogs[next]) loadCatalog(next).then(() => lang === next && notify(), () => undefined)
}

if (typeof window !== 'undefined') {
  // the system language changed (browser settings) or another tab picked one
  window.addEventListener('languagechange', recompute)
  window.addEventListener('storage', (e) => {
    if (e.key !== STORAGE_KEY) return
    pref = readPref()
    recompute()
  })
}

/** The resolved interface language. */
export const getLanguage = (): Lang => lang
/** What the person picked (system unless they chose one). */
export const getLanguagePref = (): LangPref => pref

export function setLanguagePref(next: LangPref) {
  pref = next
  try {
    localStorage.setItem(STORAGE_KEY, next)
  } catch {
    /* not remembered: still applies for this tab */
  }
  recompute()
}

export function subscribeLanguage(fn: () => void): () => void {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}

/** BCP 47 locale for Intl formatting in the active language. */
export const localeOf = (l: Lang): string => (l === 'ru' ? 'ru-RU' : 'en-US')
export const locale = (): string => localeOf(lang)

/**
 * `n` per cent (0–100) in the active language's typography: «42 %» (a
 * no-break space) in Russian, “42%” in English. The i18n strings with a
 * {n} % follow the same rule.
 */
export const formatPercent = (n: number): string =>
  new Intl.NumberFormat(locale(), { style: 'percent', maximumFractionDigits: 0 }).format(n / 100)

const pluralRules = new Map<Lang, Intl.PluralRules>()
/** The plural form of `forms` for `n` in `l` (Intl.PluralRules: ru one/few/many, en one/other). */
export function pluralForm(l: Lang, forms: Plural, n: number): string {
  let rules = pluralRules.get(l)
  if (!rules) pluralRules.set(l, (rules = new Intl.PluralRules(localeOf(l))))
  const cat = rules.select(n) as keyof Plural
  return forms[cat] ?? forms.other ?? forms.many ?? forms.one
}

function interpolate(s: string, params?: Params): string {
  if (!params) return s
  return s.replace(/\{(\w+)\}/g, (m, name: string) => (name in params ? String(params[name]) : m))
}

/** Translates `key` into `l`: `{name}` placeholders come from `params`; plural entries pick by `params.count`. */
export function translate(l: Lang, key: Key, params?: Params): string {
  const entry = catalogs[l]?.[key] ?? catalogs.ru?.[key] ?? catalogs.en?.[key]
  if (entry === undefined) return String(key)
  const s = typeof entry === 'string' ? entry : pluralForm(l, entry, Number(params?.count ?? 0))
  return interpolate(s, params)
}

/** Translates into the active language (outside React, or inside a component that called useLang/useT). */
export const t: TFunction = (key, params) => translate(lang, key, params)

// what a component renders from: the language, and whether its catalog is in yet
const snapshot = () => (catalogs[lang] ? lang : `${lang}…`)

/** The active language; the component re-renders when it changes. */
export function useLang(): Lang {
  useSyncExternalStore(subscribeLanguage, snapshot, snapshot)
  return lang
}

/** `t` bound to the active language; a new function after a change (and once its catalog is in), so memo deps see it. */
export function useT(): TFunction {
  const s = useSyncExternalStore(subscribeLanguage, snapshot, snapshot)
  return useMemo<TFunction>(() => {
    const l = s.slice(0, 2) as Lang
    return (key, params) => translate(l, key, params)
  }, [s])
}
