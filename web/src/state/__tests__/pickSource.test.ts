import { describe, expect, it } from 'vitest'
import type { EpisodeSource } from '../../api/types'
import { bestSource, choiceOf, groupSources, pickSource, qualityRank } from '../pickSource'

function src(p: Partial<EpisodeSource> & { meta?: EpisodeSource['meta'] }): EpisodeSource {
  return { kind: 'online', ref: 'r', labels: [], firstSeen: '2026-01-01T00:00:00Z', meta: {}, ...p }
}

describe('pickSource', () => {
  const libEn = src({ kind: 'library', ref: 'item:1', quality: '1080p', meta: { lang: 'en', itemId: 1 } })
  const onA = src({ kind: 'online', ref: 'a', provider: 'Alpha', quality: '1080p', meta: { providerId: 'alpha', voice: 'Studio One', lang: 'ru' } })
  const onB = src({ kind: 'online', ref: 'b', provider: 'Beta', quality: '720p', meta: { providerId: 'beta', voice: 'Studio One', lang: 'ru' } })
  const onC = src({ kind: 'online', ref: 'c', provider: 'Gamma', quality: '1080p', meta: { providerId: 'gamma', voice: 'Studio Two', lang: 'ru' } })

  it('nearest match: exact', () => {
    const choice = { kind: 'online' as const, provider: 'beta', voice: 'Studio One', lang: 'ru' }
    expect(pickSource([libEn, onA, onB, onC], choice)).toBe(onB)
  })
  it('nearest match: same dub elsewhere', () => {
    const choice = { kind: 'online' as const, provider: 'zeta', voice: 'Studio One' }
    expect(pickSource([libEn, onC, onB], choice)).toBe(onB)
  })
  it('nearest match: same language', () => {
    const choice = { kind: 'online' as const, provider: 'zeta', voice: 'Nobody', lang: 'ru' }
    expect(pickSource([libEn, onC], choice)).toBe(onC)
  })
  it('nearest match: best', () => {
    const choice = { kind: 'online' as const, provider: 'zeta', voice: 'Nobody', lang: 'de' }
    expect(pickSource([onA, libEn], choice)).toBe(libEn)
    expect(pickSource([onA, libEn], null)).toBe(libEn)
    expect(pickSource([], choice)).toBeNull()
  })
  it('kinds sort on one scale: library, an addon\'s order, online at 20, unknown last', () => {
    // an addon's kinds alpha (10) and beta (30), from its episodeActions entry
    const order = { alpha: 10, beta: 30 }
    const u = src({ kind: 'gamma', quality: '2160p' })
    const b = src({ kind: 'beta', quality: '2160p' })
    const a = src({ kind: 'alpha', quality: '720p' })
    const o = src({ kind: 'online', quality: '1080p' })
    const l = src({ kind: 'library', quality: '480p' })
    expect(bestSource([u, b, o, a, l], order)).toBe(l)
    expect(bestSource([u, b, o, a], order)).toBe(a)
    expect(bestSource([u, b, o], order)).toBe(o)
    expect(bestSource([u, b], order)).toBe(b)
    expect(bestSource([u], order)).toBe(u)
    // with no addon's order its kinds are unknown: after online
    expect(bestSource([a, o])).toBe(o)
    // an order of 0 or below never outranks the library
    const hiA = src({ kind: 'alpha', quality: '2160p' })
    expect(bestSource([hiA, l], { alpha: -1 })).toBe(l)
    expect(bestSource([hiA, l], { alpha: 0 })).toBe(l)
    // the source sheet's rows follow the same scale
    expect(groupSources([u, b, o, a, l], '', order)[0]!.dubs[0]!.items).toEqual([l, a, o, b, u])
    const hi = src({ kind: 'online', quality: '1080p' })
    expect(bestSource([o, hi, src({ kind: 'online', quality: '720p' })])).toBe(o)
  })
  it('exact provider at lower quality beats other provider when quality missing', () => {
    const lo = src({ ref: 'lo', provider: 'Alpha', quality: '720p', meta: { providerId: 'alpha', voice: 'V' } })
    const other = src({ ref: 'ot', provider: 'Beta', quality: '1080p', meta: { providerId: 'beta', voice: 'V' } })
    const choice = { kind: 'online' as const, provider: 'alpha', voice: 'V', quality: '1080p' }
    expect(pickSource([other, lo], choice)).toBe(lo)
  })
  it('exact prefers the chosen quality', () => {
    const hi = src({ ref: 'hi', quality: '1080p', meta: { providerId: 'alpha', voice: 'V' } })
    const lo = src({ ref: 'lo', quality: '720p', meta: { providerId: 'alpha', voice: 'V' } })
    expect(pickSource([hi, lo], { kind: 'online', provider: 'alpha', voice: 'V', quality: '720p' })).toBe(lo)
  })
  it('choiceOf describes a source and round-trips through pickSource', () => {
    expect(choiceOf(onA)).toEqual({ kind: 'online', provider: 'alpha', voice: 'Studio One', lang: 'ru', quality: '1080p' })
    const pin = src({ kind: 'pinned', provider: 'Indexer', meta: { studios: ['S'] } })
    expect(choiceOf(pin).provider).toBe('Indexer')
    expect(choiceOf(pin).voice).toBe('S')
    expect(pickSource([onA, onB], choiceOf(onB))).toBe(onB)
  })
})

describe('qualityRank', () => {
  it('orders resolutions', () => {
    expect(qualityRank('4K')).toBeGreaterThan(qualityRank('1080p'))
    expect(qualityRank('2160p')).toBe(qualityRank('4K'))
    expect(qualityRank('1080p')).toBeGreaterThan(qualityRank('720p'))
    expect(qualityRank('720p')).toBeGreaterThan(qualityRank('480p'))
    expect(qualityRank('480p')).toBeGreaterThan(qualityRank(undefined))
    expect(qualityRank('weird')).toBe(qualityRank(undefined))
  })
})

describe('pickSource with a preferred way', () => {
  const lib1 = src({ kind: 'library', ref: 'item:1', quality: '720p' })
  const on = src({ kind: 'online', ref: 'o', quality: '1080p' })
  const alpha = src({ kind: 'alpha', ref: 'a:1:0', quality: '1080p' })
  const beta = src({ kind: 'beta', ref: 'b', quality: '2160p' })
  const order = { alpha: 10, beta: 30 }
  it('no choice: the preferred kinds win over the default order', () => {
    expect(pickSource([on, beta], null, ['alpha', 'beta'], order)).toBe(beta)
    expect(pickSource([on, alpha, beta], null, ['alpha', 'beta'], order)).toBe(alpha)
    expect(pickSource([alpha, on], null, ['online'], order)).toBe(on)
  })
  it('the library stays first', () => {
    expect(pickSource([on, lib1], null, ['online'], order)).toBe(lib1)
  })
  it('none of the preferred kinds: the best', () => {
    expect(pickSource([alpha, beta], null, ['online'], order)).toBe(alpha)
  })
  it('a saved choice beats the preference', () => {
    expect(pickSource([on, alpha], { kind: 'online' }, ['alpha'], order)).toBe(on)
  })
})

describe('groupSources', () => {
  it('groups by language then dub', () => {
    const a = src({ ref: 'a', meta: { lang: 'ru', voice: 'One' } })
    const b = src({ ref: 'b', meta: { lang: 'ru', studios: ['One'] } })
    const c = src({ ref: 'c', meta: { lang: 'ru', audio: 'MVO' } })
    const d = src({ ref: 'd', meta: { lang: 'en' } })
    const e = src({ ref: 'e', meta: { lang: 'en' } })
    const f = src({ ref: 'f', meta: { lang: 'en' } })
    const h = src({ ref: 'h', meta: { lang: 'en' } })
    const g = groupSources([a, b, c, d, e, f, h])
    expect(g.map((x) => x.lang)).toEqual(['en', 'ru'])
    const ru = g[1]!
    expect(ru.dubs.map((x) => x.dub)).toEqual(['One', 'MVO'])
    expect(ru.dubs[0]!.items).toEqual([a, b])
    expect(g[0]!.dubs).toEqual([{ dub: '', items: [d, e, f, h] }])
  })
  it('the UI language comes first', () => {
    const ru = src({ ref: 'r', meta: { lang: 'ru' } })
    const en1 = src({ ref: 'e1', meta: { lang: 'en' } })
    const en2 = src({ ref: 'e2', meta: { lang: 'en' } })
    expect(groupSources([en1, en2, ru], 'ru').map((x) => x.lang)).toEqual(['ru', 'en'])
  })
  it('pinned group comes first', () => {
    const ru1 = src({ ref: 'r1', meta: { lang: 'ru', voice: 'One' } })
    const ru2 = src({ ref: 'r2', meta: { lang: 'ru', voice: 'One' } })
    const ruPin = src({ kind: 'pinned', ref: 'pin:1:0', meta: { lang: 'ru', voice: 'Two' } })
    const pin = src({ kind: 'pinned', ref: 'pin:1:1' })
    // a pinned release of unknown language: its group leads, the UI language's next
    expect(groupSources([ru1, ru2, pin], 'ru').map((x) => x.lang)).toEqual(['', 'ru'])
    // inside a language, the pinned dub leads however small
    expect(groupSources([ru1, ru2, ruPin], 'ru')[0]!.dubs.map((d) => d.dub)).toEqual(['Two', 'One'])
  })
  it('unknown language last as «Другое»', () => {
    const u1 = src({ ref: 'u1' })
    const u2 = src({ ref: 'u2' })
    const ja = src({ ref: 'j', meta: { lang: 'ja' } })
    const g = groupSources([u1, u2, ja])
    expect(g.map((x) => x.lang)).toEqual(['ja', ''])
  })
})
