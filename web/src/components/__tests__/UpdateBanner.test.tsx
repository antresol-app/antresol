// The administrator's «Вышла версия …» banner: asks /api/updates at most
// every six hours per device, hides a release for good on «Скрыть» (the next
// one shows), says nothing to a profile that is not an admin, and is not on
// «Здоровье» itself.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { session } from '../../state/session'
import { CHECK_EVERY_MS, newerRelease } from '../../state/updateCheck'
import { UpdateBanner } from '../UpdateBanner'

const ADMIN = { id: 1, name: 'Админ', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null }
const KID = { ...ADMIN, id: 2, name: 'Дети', isAdmin: false, isKid: true }
let core = { version: '0.8.22', commit: 'abc', latest: 'v0.9.0', checked: true, newer: true, docker: true }
let asked = 0
let who = ADMIN
const json = (body: unknown, status = 200) => Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

beforeEach(() => {
  localStorage.clear()
  asked = 0
  who = ADMIN
  core = { version: '0.8.22', commit: 'abc', latest: 'v0.9.0', checked: true, newer: true, docker: true }
  vi.spyOn(globalThis, 'fetch').mockImplementation((input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    if (url.endsWith('/api/login')) return json(who)
    if (url === '/api/updates') {
      asked++
      return json(core)
    }
    return json({ error: 'unexpected ' + url }, 500)
  })
})
afterEach(() => {
  session.reset()
  vi.restoreAllMocks()
  localStorage.clear()
})

const open = (path = '/') =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <UpdateBanner />
    </MemoryRouter>,
  )

describe('the update banner', () => {
  it('tells the admin, links to «Здоровье» and hides that release on «Скрыть»', async () => {
    await session.login(ADMIN.id)
    const { unmount } = open()
    expect(await screen.findByText('Вышла версия v0.9.0')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Подробнее' })).toHaveAttribute('href', '/manage/health')
    fireEvent.click(screen.getByRole('button', { name: 'Скрыть' }))
    expect(screen.queryByText('Вышла версия v0.9.0')).toBeNull()
    unmount()
    open()
    await waitFor(() => expect(asked).toBe(1)) // kept: not asked again within six hours
    expect(screen.queryByText(/Вышла версия/)).toBeNull()
  })

  it('asks again after six hours, and a newer release shows again', async () => {
    await newerRelease(Date.now() - CHECK_EVERY_MS - 1000)
    core = { ...core, latest: 'v0.9.1' }
    expect(await newerRelease(Date.now())).toBe('v0.9.1')
    expect(asked).toBe(2)
    expect(await newerRelease(Date.now())).toBe('v0.9.1')
    expect(asked).toBe(2)
  })

  it('says nothing when the server is current or the check failed', async () => {
    core = { ...core, newer: false, latest: 'v0.8.22' }
    expect(await newerRelease()).toBe('')
    localStorage.clear()
    core = { ...core, checked: false, latest: '' }
    expect(await newerRelease()).toBe('')
    expect(localStorage.getItem('vault.updateCheck')).toBeNull()
  })

  it('is not for a profile that is not an admin, nor on «Здоровье»', async () => {
    who = KID
    await session.login(KID.id)
    const kid = open()
    await new Promise((r) => setTimeout(r, 20))
    expect(asked).toBe(0)
    kid.unmount()
    session.reset()
    who = ADMIN
    await session.login(ADMIN.id)
    open('/manage/health')
    await waitFor(() => expect(asked).toBe(1))
    expect(screen.queryByText(/Вышла версия/)).toBeNull()
  })
})
