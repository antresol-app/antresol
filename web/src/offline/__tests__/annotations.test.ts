import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Annotation } from '../../api/types'
import { session } from '../../state/session'
import {
  cachedAnnotations,
  deleted,
  edited,
  failedAnnotations,
  liveAnnotations,
  lostAnnotationChanges,
  makeAnnotation,
  mergeAnnotations,
  PENDING_MAX,
  pendingAnnotations,
  refreshAnnotations,
  sendPendingAnnotations,
  writeAnnotation,
} from '../annotations'

const json = (v: unknown, init: ResponseInit = {}) => new Response(JSON.stringify(v), { status: 200, headers: { 'Content-Type': 'application/json' }, ...init })

/** A server with the real rule (progress.applies): its own time and revisions. */
let server: Annotation[] = []
let rev = 0
let down = false
let refuse = false
const SERVER_NOW = '2026-11-02T12:00:00.000Z'

function apply(a: Annotation): Annotation {
  const cur = server.find((x) => x.id === a.id)
  const ok = !cur || (cur.deleted ? a.deleted && a.rev >= cur.rev : a.rev >= cur.rev || a.deleted || a.updatedAt > cur.updatedAt)
  if (!ok) return cur!
  const stored = { ...a, rev: ++rev, updatedAt: SERVER_NOW, createdAt: cur?.createdAt ?? SERVER_NOW }
  server = [...server.filter((x) => x.id !== a.id), stored]
  return stored
}

beforeEach(async () => {
  localStorage.clear()
  server = []
  rev = 0
  down = false
  refuse = false
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url === '/api/me' || url === '/api/login') return json({ id: 1, name: 'Александр', isAdmin: true, isKid: false, avatar: 'grad-1' })
      if (down) throw new TypeError('Failed to fetch')
      if (url === '/api/annotations/9' && (init?.method ?? 'GET') === 'GET') return json({ annotations: server })
      if (url.startsWith('/api/annotations/9/') && init?.method === 'PUT') {
        if (refuse) return json({ error: 'Слишком много заметок в этой книге' }, { status: 400 })
        return json(apply(JSON.parse(String(init.body)) as Annotation))
      }
      return new Response('{"error":"Не найдено"}', { status: 404 })
    }),
  )
  session.reset()
  await session.login(1)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

const at = (iso: string) => new Date(iso)

describe('annotations', () => {
  it('server rows merge by revision, a tombstone over its own revision', () => {
    const a = { ...makeAnnotation({ kind: 'highlight', locator: 'epubcfi(/6/4!/4,/1:0,/1:3)', fraction: 0.1, excerpt: 'Раз' }), rev: 3 }
    const later = { ...edited(a, { color: 'pink' }), rev: 5 }
    const gone = { ...deleted(a), rev: 5 }
    expect(mergeAnnotations([later], [a])).toEqual([later])
    expect(mergeAnnotations([later], [gone])).toEqual([gone])
    expect(liveAnnotations(mergeAnnotations([gone]))).toEqual([])
    expect(gone.note).toBe('')
  })

  it('a new annotation has a random id, rev 0, a cut excerpt and note, and no colour when a bookmark', () => {
    const h = makeAnnotation({ kind: 'highlight', locator: 'epubcfi(/6/4)', fraction: 2, excerpt: 'я'.repeat(1500), note: 'n'.repeat(10_500) })
    const b = makeAnnotation({ kind: 'bookmark', locator: 'epubcfi(/6/4)', fraction: 0.5, color: 'green' })
    expect(h.id).toMatch(/^[0-9a-f]{32}$/)
    expect(h.id).not.toBe(b.id)
    expect(h.rev).toBe(0)
    expect(Array.from(h.excerpt)).toHaveLength(1000)
    expect(h.note).toHaveLength(10_000)
    expect(h.color).toBe('yellow')
    expect(h.fraction).toBe(1)
    expect(b.color).toBe('')
  })

  it('a change made without the server is kept here and sent when it is back; the stored row is adopted', async () => {
    down = true
    const b = makeAnnotation({ kind: 'bookmark', locator: 'epubcfi(/6/8!/4/2/1:0)', fraction: 0.4 }, at('2030-01-01T00:00:00Z')) // a clock years ahead
    await writeAnnotation(9, b)
    expect(liveAnnotations(cachedAnnotations(9))).toEqual([b])
    expect(pendingAnnotations()).toBe(1)

    down = false
    await sendPendingAnnotations()
    expect(pendingAnnotations()).toBe(0)
    const [mine] = cachedAnnotations(9)
    expect(mine).toMatchObject({ id: b.id, rev: 1, updatedAt: SERVER_NOW })
  })

  it('a delete from a device with a slow clock wins; an older edit does not bring it back', async () => {
    const h = makeAnnotation({ kind: 'note', locator: 'epubcfi(/6/4!/4,/1:0,/1:3)', fraction: 0.2, note: 'мысль' })
    await writeAnnotation(9, h)
    const stored = cachedAnnotations(9)[0]!
    // another device (fast clock) edits it
    apply({ ...stored, note: 'там', updatedAt: '2031-01-01T00:00:00.000Z' })
    // this one (clock years behind) deletes what it has: the delete is the latest write the server gets
    await writeAnnotation(9, deleted(stored, at('2001-01-01T00:00:00Z')))
    expect(server[0]!.deleted).toBe(true)
    expect(liveAnnotations(await refreshAnnotations(9))).toEqual([])
    // an offline edit made on the revision before the delete, sent later
    await writeAnnotation(9, edited(stored, { note: 'старое' }, at('2032-01-01T00:00:00Z')))
    expect(liveAnnotations(cachedAnnotations(9))).toEqual([])
  })

  it('a refused change stays here, marked failed, and is not sent again', async () => {
    refuse = true
    const b = makeAnnotation({ kind: 'bookmark', locator: 'page:3', fraction: 0.1 })
    await writeAnnotation(9, b)
    expect(failedAnnotations(9)).toEqual(new Set([b.id]))
    expect(liveAnnotations(cachedAnnotations(9)).map((a) => a.id)).toEqual([b.id])
    expect(pendingAnnotations()).toBe(0)
    refuse = false
    await sendPendingAnnotations()
    expect(server).toEqual([])
  })

  it('the queue keeps one change per annotation and never drops a delete', async () => {
    down = true
    const first = makeAnnotation({ kind: 'bookmark', locator: 'page:1', fraction: 0 })
    await writeAnnotation(9, first)
    await writeAnnotation(9, deleted(first))
    for (let i = 0; i < PENDING_MAX + 2; i++) await writeAnnotation(9, makeAnnotation({ kind: 'bookmark', locator: `page:${i + 2}`, fraction: 0 }))
    const pending = JSON.parse(localStorage.getItem('vault.pending-annotations.v2') ?? '[]') as { a: Annotation }[]
    expect(pending).toHaveLength(PENDING_MAX)
    expect(pending.some((p) => p.a.id === first.id && p.a.deleted)).toBe(true)
    expect(lostAnnotationChanges()).toBe(3)
  })
})
