// What can reach the screen as a raw key or as a text in the wrong language:
// every catalog namespace file has its twin with the same keys, every key
// the code names (a literal, or the fixed start of a template that is cast to
// Key) is in both languages, and no Russian sentence is left in English
// letters. The type of `t` already checks a literal key; these catch the
// dynamic ones (`t(`acct.err.${x}` as Key)`) and a copied English text.
import { describe, expect, it } from 'vitest'
import { ru } from '../ru'
import { en } from '../en'
import type { Entry } from '../types'

const ruFiles = import.meta.glob<{ default: Record<string, Entry> }>('../ru/*.ts', { eager: true })
const enFiles = import.meta.glob<{ default: Record<string, Entry> }>('../en/*.ts', { eager: true })
const sources = import.meta.glob<string>(['../../**/*.{ts,tsx}', '!../../**/__tests__/**', '!../../**/*.test.*', '!../../i18n/{ru,en}/**', '!../../vendor/**'], {
  eager: true,
  query: '?raw',
  import: 'default',
})

const nameOf = (path: string) => path.replace(/^.*\//, '')
const text = (v: Entry) => (typeof v === 'string' ? v : Object.values(v).join(' '))

describe('catalog files', () => {
  const ruByName = new Map(Object.entries(ruFiles).map(([p, m]) => [nameOf(p), m.default]))
  const enByName = new Map(Object.entries(enFiles).map(([p, m]) => [nameOf(p), m.default]))

  it('every Russian namespace file has an English one with the same keys, and the other way round', () => {
    const problems: string[] = []
    for (const [name, cat] of ruByName) {
      const twin = enByName.get(name)
      if (!twin) {
        problems.push(`${name}: no English file`)
        continue
      }
      for (const k of Object.keys(cat)) if (!(k in twin)) problems.push(`${name}: ${k} missing in en`)
      for (const k of Object.keys(twin)) if (!(k in cat)) problems.push(`${name}: ${k} missing in ru`)
    }
    for (const name of enByName.keys()) if (!ruByName.has(name)) problems.push(`${name}: no Russian file`)
    expect(problems).toEqual([])
  })

  it('no text is empty in either language', () => {
    const empty = [...Object.entries(ru), ...Object.entries(en)].filter(([, v]) => text(v as Entry).trim() === '').map(([k]) => k)
    expect(empty).toEqual([])
  })

  it('no Russian text is left in English letters: a sentence without a Cyrillic letter is an untranslated copy', () => {
    // brands, font names and protocol terms stay as they are (capitalised words); a sentence has three
    // words and a lower-case one among them
    const sentence = (s: string) => {
      const words = s.replace(/\{\w+\}/g, '').match(/[A-Za-z][A-Za-z'’-]{2,}/g) ?? []
      return words.length >= 3 && words.some((w) => /^[a-z]/.test(w))
    }
    const left = Object.entries(ru as Record<string, Entry>)
      .filter(([, v]) => !/[А-Яа-яЁё]/.test(text(v)) && sentence(text(v)))
      .map(([k]) => k)
    expect(left).toEqual([])
  })
})

describe('English text', () => {
  it('uses English quotes: «…» is Russian punctuation', () => {
    const quoted = Object.entries(en as Record<string, Entry>)
      .filter(([, v]) => /[«»]/.test(text(v)))
      .map(([k]) => k)
    expect(quoted).toEqual([])
  })
})

describe('keys named in the code', () => {
  const namespaces = new Set(Object.keys(ru).map((k) => k.split('.')[0]))
  const all = Object.keys(ru)
  // the names of server events and the paths of data fields look like keys and are not
  const notKeys = new Set(['addons.changed', 'import.progress', 'item.id'])
  const literal = /(['"`])([a-z][A-Za-z0-9]*(?:\.[A-Za-z0-9-]+)+)\1/g
  const dynamic = /`([a-z][A-Za-z0-9]*(?:\.[A-Za-z0-9-]+)*\.)\$\{/g

  it('every literal that is shaped like a key and starts with a namespace is a key of both languages', () => {
    const missing = new Set<string>()
    for (const [path, src] of Object.entries(sources)) {
      for (const m of src.matchAll(literal)) {
        const k = m[2]!
        if (!namespaces.has(k.split('.')[0]!) || k in ru || notKeys.has(k)) continue
        // a file name
        if (/\.(tsx?|css|png|jpe?g|svg|json|mjs|js|html|webmanifest|woff2?|m3u8?|mp[34]|txt|csv|xml|zip)$/.test(k)) continue
        missing.add(`${path.replace('../../', '')}: ${k}`)
      }
    }
    expect([...missing]).toEqual([])
  })

  it('every template that starts with a namespace has keys under its fixed start in both languages', () => {
    const bad = new Set<string>()
    for (const [path, src] of Object.entries(sources)) {
      for (const m of src.matchAll(dynamic)) {
        const prefix = m[1]!
        if (!namespaces.has(prefix.split('.')[0]!)) continue
        if (!all.some((k) => k.startsWith(prefix))) bad.add(`${path.replace('../../', '')}: ${prefix}\${…}`)
        else if (!Object.keys(en).some((k) => k.startsWith(prefix))) bad.add(`${path.replace('../../', '')}: ${prefix}\${…} (en)`)
      }
    }
    expect([...bad]).toEqual([])
  })
})
