import { afterEach, describe, expect, it, vi } from 'vitest'
import { getHome } from '../client'
import { setLanguagePref } from '../../i18n'

describe('API requests carry the interface language', () => {
  afterEach(() => setLanguagePref('system'))

  const headerOf = async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response('{}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await getHome()
    const init = fetchMock.mock.calls[0]![1]!
    vi.unstubAllGlobals()
    return (init.headers as Record<string, string>)['Accept-Language']
  }

  it('sends Accept-Language: ru by default (the browser prefers Russian in tests)', async () => {
    expect(await headerOf()).toBe('ru')
  })

  it('sends Accept-Language: en once English is picked', async () => {
    setLanguagePref('en')
    expect(await headerOf()).toBe('en')
  })

  it('a client-side error message follows the language', async () => {
    setLanguagePref('en')
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('offline') }))
    await expect(getHome()).rejects.toThrow('Can’t reach the server. Check your connection and try again.')
    vi.unstubAllGlobals()
  })
})
