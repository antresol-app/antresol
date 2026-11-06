import { Blob as NodeBlob } from 'node:buffer'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { LOCK_PATH, OFFLINE_CACHE } from '../saved'
import { activate, api, ASSETS_CACHE, install, media, NAVIGATE_TIMEOUT_MS, navigate, resetSWForTests, routeOf, savedChanged, shell } from '../sw'
import { FakeCaches } from './fakeCaches'

const ORIGIN = 'http://vault.test'
const route = (path: string, method = 'GET', mode: RequestMode = 'cors') => routeOf(method, new URL(path, ORIGIN), mode, ORIGIN)

describe('service worker routes', () => {
  it('ignores /api/events, /rest and non-GET', () => {
    expect(route('/api/events')).toBe('skip')
    expect(route('/rest/stream.view?id=1')).toBe('skip')
    expect(route('/rest/ping')).toBe('skip')
    expect(route('/opds')).toBe('skip')
    expect(route('/opds/search?q=x')).toBe('skip')
    expect(route('/api/progress/5', 'PUT')).toBe('skip')
    expect(route('/api/music/plays', 'POST')).toBe('skip')
    expect(route('/', 'POST', 'navigate')).toBe('skip')
    expect(route('/sw.js')).toBe('skip')
    expect(routeOf('GET', new URL('https://image.tmdb.org/x.jpg'), 'no-cors', ORIGIN)).toBe('skip')
  })

  it('routes navigations, media and the API', () => {
    expect(route('/music', 'GET', 'navigate')).toBe('navigate')
    expect(route('/assets/index-abc.js')).toBe('shell')
    expect(route('/api/tracks/5/stream?accept=flac')).toBe('media')
    expect(route('/api/audio/9?accept=aac&t=30')).toBe('media')
    expect(route('/api/stream/12')).toBe('media')
    expect(route('/api/images/a.jpg')).toBe('media')
    expect(route('/api/comics/4/page/0')).toBe('media')
    expect(route('/api/music/albums/7')).toBe('api')
    expect(route('/api/comics/4/pages')).toBe('api')
  })
})

describe('service worker handlers', () => {
  const CSP = "default-src 'self'; script-src 'self'"
  let caches: FakeCaches
  let net: (url: string, init?: RequestInit) => Promise<Response>
  const self_ = window as unknown as { __VAULT_ASSETS?: string[]; __VAULT_BUILD?: string }

  beforeEach(() => {
    resetSWForTests()
    caches = new FakeCaches()
    vi.stubGlobal('caches', caches)
    vi.stubGlobal('clients', { claim: async () => undefined })
    self_.__VAULT_ASSETS = ['/assets/app-1.js', '/index.html']
    self_.__VAULT_BUILD = 'b1'
    net = async (url) =>
      url.endsWith('/assets/app-1.js')
        ? new Response('js', { headers: { 'Content-Type': 'text/javascript' } })
        : new Response('<div id="root"></div>', { headers: { 'Content-Type': 'text/html; charset=utf-8', 'Content-Security-Policy': CSP } })
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => net(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, init)),
    )
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
    delete self_.__VAULT_ASSETS
    delete self_.__VAULT_BUILD
  })

  const nav = (path = '/music') => new Request(`${ORIGIN}${path}`)

  it('serves the saved app with the server CSP when the network fails or the proxy says 502', async () => {
    await install()
    net = async () => {
      throw new TypeError('Failed to fetch')
    }
    let res = await navigate(nav())
    expect(await res.text()).toContain('id="root"')
    expect(res.headers.get('Content-Security-Policy')).toBe(CSP)
    net = async () => new Response('<h1>502 Bad Gateway</h1>', { status: 502 })
    res = await navigate(nav())
    expect(res.status).toBe(200)
    expect(res.headers.get('Content-Security-Policy')).toBe(CSP)
    // a real answer of the server is passed through
    net = async () => new Response('nope', { status: 404 })
    expect((await navigate(nav())).status).toBe(404)
  })

  it('install fails when any file of the shell fails (the old shell stays)', async () => {
    net = async (url) => (url.endsWith('/assets/app-1.js') ? new Response('gone', { status: 404 }) : new Response('<html>'))
    await expect(install()).rejects.toThrow()
  })

  it('a new build downloads only the hashed files it changed; public files every time', async () => {
    self_.__VAULT_ASSETS = ['/assets/app-1.js', '/icon.png', '/index.html']
    await install()
    const asked = () => (fetch as unknown as { mock: { calls: [Request | string][] } }).mock.calls.map(([r]) => new URL(typeof r === 'string' ? r : r.url, ORIGIN).pathname)
    expect(asked()).toEqual(['/assets/app-1.js', '/icon.png', '/'])
    vi.mocked(fetch).mockClear()
    self_.__VAULT_ASSETS = ['/assets/app-1.js', '/assets/page-2.js', '/icon.png', '/index.html']
    self_.__VAULT_BUILD = 'b2'
    await install()
    expect(asked()).toEqual(['/assets/page-2.js', '/icon.png', '/'])
  })

  it('activate keeps the previous build\'s files one more deploy, then drops them', async () => {
    const kept = async () => (await (await caches.open(ASSETS_CACHE)).keys()).map((r) => new URL(r.url).pathname).sort()
    const files = await caches.open(ASSETS_CACHE)
    for (const f of ['/assets/a-1.js', '/assets/b-1.js', '/assets/c-1.js', '/assets/stray.js']) await files.put(f, new Response('x'))
    const deploy = async (build: string, list: string[]) => {
      self_.__VAULT_BUILD = build
      self_.__VAULT_ASSETS = list
      await activate()
    }
    await deploy('b1', ['/assets/a-1.js', '/assets/b-1.js'])
    expect(await kept()).toEqual(['/assets/a-1.js', '/assets/b-1.js'])
    // b2's install brings its new file, then it activates
    await files.put('/assets/c-1.js', new Response('x'))
    await deploy('b2', ['/assets/a-1.js', '/assets/c-1.js'])
    // b-1 is b1's: an open tab of b1 may still ask for it
    expect(await kept()).toEqual(['/assets/a-1.js', '/assets/b-1.js', '/assets/c-1.js'])
    await deploy('b3', ['/assets/c-1.js'])
    expect(await kept()).toEqual(['/assets/a-1.js', '/assets/c-1.js'])
  })

  it('activate drops only shells, built files and lists of another version, never another vault-* cache', async () => {
    for (const n of ['vault-assets-v2', 'vault-meta-v9', 'vault-shell-old', OFFLINE_CACHE, 'vault-offline-v2', 'vault-downloads', 'someone-else']) await caches.open(n)
    await activate()
    const names = await caches.keys()
    for (const n of [OFFLINE_CACHE, 'vault-offline-v2', 'vault-downloads', 'someone-else']) expect(names).toContain(n)
    for (const n of ['vault-assets-v2', 'vault-meta-v9', 'vault-shell-old']) expect(names).not.toContain(n)
  })

  it('a file fetched on demand (hls.js) is kept across activations while a build names it', async () => {
    const self2 = self_ as typeof self_ & { __VAULT_ON_DEMAND?: string[] }
    const files = await caches.open(ASSETS_CACHE)
    await files.put('/assets/hls-1.js', new Response('x'))
    self2.__VAULT_ON_DEMAND = ['/assets/hls-1.js']
    for (const b of ['b1', 'b2', 'b3']) {
      self_.__VAULT_BUILD = b
      self_.__VAULT_ASSETS = [`/assets/app-${b}.js`]
      await activate()
    }
    expect(await files.match('/assets/hls-1.js')).toBeDefined()
    delete self2.__VAULT_ON_DEMAND
  })

  it('a built file not kept yet is fetched once and then served from the cache', async () => {
    const req = new Request(`${ORIGIN}/assets/app-1.js`)
    expect(await (await shell(req)).text()).toBe('js')
    expect(await (await caches.open(ASSETS_CACHE)).match('/assets/app-1.js')).toBeDefined()
    net = async () => {
      throw new TypeError('Failed to fetch')
    }
    expect(await (await shell(req)).text()).toBe('js')
  })

  it('a hanging network opens the saved app after the timeout once something is saved', async () => {
    await install()
    await (await caches.open(OFFLINE_CACHE)).put('/api/music/albums/7', new Response('{}'))
    await savedChanged()
    vi.useFakeTimers()
    net = () => new Promise<Response>(() => {})
    const p = navigate(nav())
    await vi.advanceTimersByTimeAsync(NAVIGATE_TIMEOUT_MS + 10)
    expect(await (await p).text()).toContain('id="root"')
  })

  it('saved JSON comes from the cache when the network fails or answers 503', async () => {
    await (await caches.open(OFFLINE_CACHE)).put('/api/books/9', new Response('{"saved":true}'))
    net = async () => new Response('down', { status: 503 })
    expect(await (await api(new Request(`${ORIGIN}/api/books/9`))).text()).toBe('{"saved":true}')
    net = async () => {
      throw new TypeError('Failed to fetch')
    }
    expect(await (await api(new Request(`${ORIGIN}/api/books/9`))).text()).toBe('{"saved":true}')
    // online: the server's copy, and the saved one refreshed
    net = async () => new Response('{"fresh":true}')
    expect(await (await api(new Request(`${ORIGIN}/api/books/9`))).text()).toBe('{"fresh":true}')
    expect(await (await (await caches.open(OFFLINE_CACHE)).match('/api/books/9'))!.text()).toBe('{"fresh":true}')
  })

  it('a Range request to a saved track gets 206 from the whole file', async () => {
    const body = new NodeBlob([new Uint8Array(1000)]) as unknown as Blob
    await (await caches.open(OFFLINE_CACHE)).put(
      '/api/tracks/5/stream',
      new Response(body, { headers: { 'Content-Type': 'audio/aac', 'X-Content-Duration': '20' } }),
    )
    const res = await media(new Request(`${ORIGIN}/api/tracks/5/stream?accept=aac&t=0`, { headers: { Range: 'bytes=500-' } }))
    expect(res.status).toBe(206)
    expect(res.headers.get('Content-Range')).toBe('bytes 500-999/1000')
    expect(res.headers.get('X-Content-Duration')).toBe('20')
  })

  it('while locked nothing saved is served (the list is empty)', async () => {
    const c = await caches.open(OFFLINE_CACHE)
    await c.put('/api/stream/12', new Response('epub'))
    expect([...(await savedChanged())]).toEqual(['/api/stream/12'])
    await c.put(LOCK_PATH, new Response('locked'))
    expect((await savedChanged(true)).size).toBe(0)
    await c.delete(LOCK_PATH)
    expect([...(await savedChanged())]).toEqual(['/api/stream/12'])
  })

  it('the saved index.html drops encoding, length and cookies', async () => {
    net = async () =>
      new Response('<div id="root"></div>', {
        headers: { 'Content-Type': 'text/html', 'Content-Security-Policy': CSP, 'Content-Encoding': 'gzip', 'Set-Cookie': 'a=b', 'Content-Length': '5' },
      })
    await install()
    net = async () => {
      throw new TypeError('Failed to fetch')
    }
    const res = await navigate(nav())
    expect(res.headers.get('Content-Encoding')).toBeNull()
    expect(res.headers.get('Content-Length')).toBeNull()
    expect(res.headers.get('Content-Security-Policy')).toBe(CSP)
  })
})
