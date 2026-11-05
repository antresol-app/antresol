// Kept answers (state/lastAnswer): filed under the profile and language
// that asked, never under whoever is signed in when the answer lands.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { answerSlot, lastAnswer, rememberAnswer } from '../lastAnswer'
import { session } from '../session'
import { setLanguagePref } from '../../i18n'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

describe('lastAnswer', () => {
  beforeEach(async () => {
    session.reset()
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
        const { userId } = JSON.parse(String(init?.body ?? '{}')) as { userId?: number }
        if (init?.method === 'POST' && userId) return json({ id: userId, name: 'P' + userId, avatar: 'grad-1', isAdmin: false, isKid: false })
        return new Response(null, { status: 204 })
      }),
    )
    await session.login(1)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    setLanguagePref('system')
  })

  it('an answer that lands after a switch to another profile is dropped, not kept for the new one', async () => {
    const slot = answerSlot('home all') // asked as profile 1
    await session.login(2) // straight to profile 2, no signed-out hop
    rememberAnswer(slot, { shelves: ['of profile 1'] })
    expect(lastAnswer('home all')).toBeUndefined()
  })

  it('an answer that lands after a language switch is kept under the language it was asked in', () => {
    const slot = answerSlot('home all') // asked in Russian
    setLanguagePref('en')
    rememberAnswer(slot, 'Russian shelves')
    expect(lastAnswer('home all')).toBeUndefined()
    setLanguagePref('ru')
    expect(lastAnswer('home all')).toBe('Russian shelves')
  })

  it('each library scope keeps its own answer', () => {
    rememberAnswer(answerSlot('home all'), 'shared')
    rememberAnswer(answerSlot('home mine'), 'mine')
    expect(lastAnswer('home all')).toBe('shared')
    expect(lastAnswer('home mine')).toBe('mine')
  })

  it('signing out forgets every answer', async () => {
    rememberAnswer(answerSlot('home all'), 'shelves')
    await session.logout()
    expect(session.get().user).toBeNull()
    await session.login(1)
    expect(lastAnswer('home all')).toBeUndefined()
  })
})
