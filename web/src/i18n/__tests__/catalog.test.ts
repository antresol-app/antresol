import { describe, expect, it } from 'vitest'
import { ru } from '../ru'
import { en } from '../en'
import type { Entry } from '../types'

const placeholders = (e: Entry): string[] => {
  const all = typeof e === 'string' ? [e] : Object.values(e).filter((x): x is string => typeof x === 'string')
  const names = new Set<string>()
  for (const s of all) for (const m of s.matchAll(/\{(\w+)\}/g)) names.add(m[1]!)
  return [...names].sort()
}

describe('catalogs', () => {
  const ruCat = ru as Record<string, Entry>
  const enCat = en as Record<string, Entry>

  it('every Russian key has an English value', () => {
    const missing = Object.keys(ruCat).filter((k) => {
      const v = enCat[k]
      return v === undefined || (typeof v === 'string' ? v.trim() === '' : !v.one || !v.other)
    })
    expect(missing).toEqual([])
  })

  it('English has no keys Russian lacks', () => {
    expect(Object.keys(enCat).filter((k) => !(k in ruCat))).toEqual([])
  })

  it('both sides use the same placeholders', () => {
    const diff = Object.keys(ruCat)
      .filter((k) => enCat[k] !== undefined)
      .filter((k) => placeholders(ruCat[k]!).join() !== placeholders(enCat[k]!).join())
    expect(diff).toEqual([])
  })

  it('every form of a plural entry uses the same placeholders', () => {
    const bad: string[] = []
    for (const [k, v] of [...Object.entries(ruCat), ...Object.entries(enCat)]) {
      if (typeof v === 'string') continue
      const sets = Object.values(v).map((s) => placeholders(s as string).join())
      if (new Set(sets).size > 1) bad.push(k)
    }
    expect(bad).toEqual([])
  })

  it('Russian plurals have one/few/many, English one/other', () => {
    const bad: string[] = []
    for (const [k, v] of Object.entries(ruCat)) if (typeof v !== 'string' && (!v.one || !v.few || !v.many)) bad.push(k)
    for (const [k, v] of Object.entries(enCat)) if (typeof v !== 'string' && (!v.one || !v.other)) bad.push(k)
    expect(bad).toEqual([])
  })

  it('English text has no Cyrillic except the language names', () => {
    const allowed = new Set(['common.uiLanguage.ru'])
    const cyr = Object.entries(enCat)
      .filter(([k]) => !allowed.has(k))
      .filter(([, v]) => /[А-Яа-яЁё]/.test(typeof v === 'string' ? v : Object.values(v).join(' ')))
      .map(([k]) => k)
    expect(cyr).toEqual([])
  })
})
