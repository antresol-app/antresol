import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, getMe, onUnauthorized, resetEarlyMeForTests } from '../client'
import { startEarlyMe } from '../../boot/early'
import { setLanguagePref } from '../../i18n'

const user = { id: 1, name: 'Саша', avatar: 'grad-1', isAdmin: true, isKid: false }
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

describe('GET /api/me started by the boot script', () => {
  afterEach(() => {
    delete window.__vaultEarlyMe
    resetEarlyMeForTests()
    vi.unstubAllGlobals()
    setLanguagePref('system')
  })

  it('the first getMe takes the early answer, without a request of its own; the next one asks again', async () => {
    const fetchMock = vi.fn(async () => json({ ...user, name: 'second' }))
    vi.stubGlobal('fetch', fetchMock)
    window.__vaultEarlyMe = Promise.resolve(json(user))
    expect(await getMe()).toEqual(user)
    expect(fetchMock).not.toHaveBeenCalled()
    expect(window.__vaultEarlyMe).toBeNull() // taken: a late boot script will not ask again
    expect((await getMe()).name).toBe('second')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('an early network failure is the offline ApiError (status 0), as for a request of its own', async () => {
    window.__vaultEarlyMe = Promise.reject(new TypeError('Failed to fetch'))
    const err = await getMe().catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(0)
  })

  it('an early 401 is an ApiError 401 and does not count as an expired session', async () => {
    const expired = vi.fn()
    onUnauthorized(expired)
    window.__vaultEarlyMe = Promise.resolve(json({ error: 'Нужно войти' }, 401))
    const err = await getMe().catch((e: unknown) => e)
    expect((err as ApiError).status).toBe(401)
    expect((err as ApiError).message).toBe('Нужно войти')
    expect(expired).not.toHaveBeenCalled()
    onUnauthorized(null)
  })

  it('the boot script sends what request() sends (credentials, Accept, Accept-Language of the picked language)', async () => {
    const calls: [string, RequestInit][] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => (calls.push([url, init]), json(user))))
    for (const pref of ['system', 'en', 'ru'] as const) {
      setLanguagePref(pref)
      calls.length = 0
      delete window.__vaultEarlyMe
      resetEarlyMeForTests()
      startEarlyMe()
      await window.__vaultEarlyMe
      delete window.__vaultEarlyMe
      await getMe()
      expect(calls).toHaveLength(2)
      const [[bootUrl, boot], [appUrl, app]] = calls as [[string, RequestInit], [string, RequestInit]]
      expect(bootUrl).toBe(appUrl)
      expect(boot.method ?? 'GET').toBe(app.method)
      expect(boot.credentials).toBe(app.credentials)
      expect(boot.headers).toEqual(app.headers)
    }
  })

  it('the boot script follows the browser languages when nothing is picked (the shared rule: first ru-… or en-…, else en)', async () => {
    const seen: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) => (seen.push((init.headers as Record<string, string>)['Accept-Language']!), json(user))))
    const langs = (l: string[]) => Object.defineProperty(window.navigator, 'languages', { configurable: true, get: () => l })
    try {
      for (const l of [['de-DE', 'en-GB'], ['fr'], ['ru-RU']]) {
        langs(l)
        delete window.__vaultEarlyMe
        startEarlyMe()
      }
    } finally {
      langs(['ru-RU', 'ru'])
    }
    expect(seen).toEqual(['en', 'en', 'ru'])
  })

  it('a boot script that runs after the first getMe neither asks nor leaves its answer for a later getMe', async () => {
    const fetchMock = vi.fn(async () => json(user))
    vi.stubGlobal('fetch', fetchMock)
    await getMe() // the boot script has not run yet: the app asks itself
    expect(fetchMock).toHaveBeenCalledTimes(1)
    startEarlyMe() // late: the first getMe is past
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(window.__vaultEarlyMe).toBeFalsy()
    await getMe() // e.g. after editing the profile: a fresh request, not the page-load answer
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('only the first getMe looks at the boot answer', async () => {
    const fetchMock = vi.fn(async () => json({ ...user, name: 'fresh' }))
    vi.stubGlobal('fetch', fetchMock)
    window.__vaultEarlyMe = Promise.resolve(json(user))
    expect((await getMe()).name).toBe('Саша')
    window.__vaultEarlyMe = Promise.resolve(json({ ...user, name: 'stale' }))
    expect((await getMe()).name).toBe('fresh')
  })
})
