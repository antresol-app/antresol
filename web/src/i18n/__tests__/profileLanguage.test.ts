// The interface language follows the profile when the person picked none
// here: the pick, else the profile's `effective` from the server, else the
// browser's languages. The picker saves the choice for the profile.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { getLanguage, resolveInterfaceLanguage, setLanguagePref, setProfileLanguage } from '..'
import { chooseInterfaceLanguage } from '../../state/uiLanguage'

afterEach(() => setProfileLanguage(null))

describe('the interface language', () => {
  it('resolves: the pick, else the profile, else the browser', () => {
    expect(resolveInterfaceLanguage('en', 'ru', ['ru-RU'])).toBe('en')
    expect(resolveInterfaceLanguage('system', 'ru', ['en-US'])).toBe('ru')
    expect(resolveInterfaceLanguage('system', null, ['en-US', 'ru'])).toBe('en')
    expect(resolveInterfaceLanguage('system', null, ['de'])).toBe('en')
  })

  it('a Russian profile in an English browser shows Russian; the person’s pick still wins', () => {
    const langs = vi.spyOn(window.navigator, 'languages', 'get').mockReturnValue(['en-US', 'en'])
    window.dispatchEvent(new Event('languagechange'))
    expect(getLanguage()).toBe('en')
    setProfileLanguage('ru')
    expect(getLanguage()).toBe('ru')
    setLanguagePref('en')
    expect(getLanguage()).toBe('en')
    setLanguagePref('system')
    expect(getLanguage()).toBe('ru')
    setProfileLanguage('')
    expect(getLanguage()).toBe('en')
    langs.mockRestore()
    window.dispatchEvent(new Event('languagechange'))
  })

  it('the picker saves the choice for the profile («Как в системе»: the profile’s own)', async () => {
    const calls: { url: string; method?: string; body?: unknown }[] = []
    vi.spyOn(globalThis, 'fetch').mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), method: init?.method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      const body = init?.method === 'PUT' ? null : JSON.stringify({ language: '', effective: 'ru' })
      return Promise.resolve(new Response(body, { status: init?.method === 'PUT' ? 204 : 200, headers: { 'Content-Type': 'application/json' } }))
    })
    chooseInterfaceLanguage('en')
    chooseInterfaceLanguage('system')
    await vi.waitFor(() => expect(calls.filter((c) => c.method === 'GET').length).toBe(2))
    expect(calls.filter((c) => c.method === 'PUT').map((c) => [c.url, c.body])).toEqual([
      ['/api/me/ui-language', { language: 'en' }],
      ['/api/me/ui-language', { language: '' }],
    ])
  })
})
