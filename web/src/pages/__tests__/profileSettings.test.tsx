// A profile's own «Настройки» (pages/ProfileSettings.tsx) and the slimmer
// profile menu that leads to it: the language, «Скрывать прогресс», the
// sheets, what a kid gets, and that the menu no longer holds settings.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import type { User } from '../../api/types'
import { ProfileMenu } from '../../components/ProfileMenu'
import { getLanguagePref, setLanguagePref } from '../../i18n'
import { session, useSession } from '../../state/session'
import { ProfileSettings } from '../ProfileSettings'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const adult: User = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const kid: User = { ...adult, id: 3, name: 'Вася', isKid: true, maxRating: '12+' }

type Call = [string, string | undefined, unknown]
function serve(user: User, extra?: (url: string) => Response | undefined) {
  const calls: Call[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      calls.push([url, init?.method, init?.body ? JSON.parse(String(init.body)) : undefined])
      const r = extra?.(url)
      if (r) return r
      if (url.endsWith('/api/login')) return json(user)
      if (url.endsWith('/api/me') && init?.method === 'PATCH') return json({ ...user, ...JSON.parse(String(init.body)) })
      if (url.endsWith('/api/me/app-password')) return json({ exists: false, createdAt: null })
      if (url.endsWith('/api/me/ui-language')) return json({ language: '' })
      return json({})
    }),
  )
  return calls
}

function Where() {
  return <span data-testid="where">{useLocation().pathname}</span>
}
function Menu() {
  const { user } = useSession()
  return user ? <ProfileMenu user={user} /> : null
}

async function open(user: User, extra?: (url: string) => Response | undefined) {
  const calls = serve(user, extra)
  await session.login(user.id)
  render(
    <MemoryRouter initialEntries={['/settings']}>
      <Routes>
        <Route path="/settings" element={<ProfileSettings />} />
        <Route path="*" element={<Where />} />
      </Routes>
    </MemoryRouter>,
  )
  return calls
}

beforeEach(() => session.reset())
afterEach(() => {
  vi.unstubAllGlobals()
  session.reset()
  setLanguagePref('system')
})

describe('ProfileSettings', () => {
  it('an adult sees the whole list, with short hints', async () => {
    await open(adult)
    expect(screen.getByRole('heading', { name: 'Настройки', level: 1 })).toBeInTheDocument()
    for (const name of ['Звук и субтитры', 'Письма на почту', 'Пароль для приложений', 'Устройства', 'О сервере']) {
      expect(screen.getByRole('button', { name: new RegExp(name) })).toBeInTheDocument()
    }
    expect(screen.getByRole('switch', { name: 'Скрывать прогресс просмотра' })).toBeInTheDocument()
    // no server-wide settings here
    expect(screen.queryByText(/Качество видео|Мои языки|Kindle/)).toBeNull()
  })

  it('a kid gets the language, apps and «О сервере», not sound, mail or hiding progress', async () => {
    await open(kid)
    expect(screen.getByRole('button', { name: /Пароль для приложений/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /О сервере/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Звук и субтитры/ })).toBeNull()
    expect(screen.queryByRole('button', { name: /Письма на почту/ })).toBeNull()
    expect(screen.queryByRole('switch')).toBeNull()
  })

  it('«Язык интерфейса» → English re-renders the page and remembers it in this browser', async () => {
    await open(adult)
    expect(document.documentElement.lang).toBe('ru')
    await userEvent.click(screen.getByRole('button', { name: /Язык интерфейса/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'English' }))
    expect(await screen.findByRole('heading', { name: 'Settings', level: 1 })).toBeInTheDocument()
    expect(getLanguagePref()).toBe('en')
    expect(localStorage.getItem('vault.uiLanguage')).toBe('en')
    expect(document.documentElement.lang).toBe('en')
    expect(screen.getByRole('button', { name: /Audio & Subtitles/ })).toBeInTheDocument()
  })

  it('«Скрывать прогресс просмотра»: an adult turns it on for itself', async () => {
    const calls = await open(adult)
    const sw = screen.getByRole('switch', { name: 'Скрывать прогресс просмотра' })
    expect(sw).toHaveAttribute('aria-checked', 'false')
    await userEvent.click(sw)
    await waitFor(() => expect(screen.getByRole('switch', { name: 'Скрывать прогресс просмотра' })).toHaveAttribute('aria-checked', 'true'))
    expect(calls.at(-1)).toEqual(['/api/me', 'PATCH', { hideProgress: true }])
  })

  it('«Пароль для приложений» warns a passwordless profile that apps work only at home', async () => {
    await open(adult)
    fireEvent.click(screen.getByRole('button', { name: /Пароль для приложений/ }))
    expect(await screen.findByTestId('app-password-home-only')).toHaveTextContent('Вне дома приложения не подключатся')
  })

  it('no home-only note for a profile with a password', async () => {
    await open({ ...adult, hasPassword: true })
    fireEvent.click(screen.getByRole('button', { name: /Пароль для приложений/ }))
    expect(await screen.findByRole('dialog', { name: 'Пароль для приложений' })).toBeInTheDocument()
    expect(screen.queryByTestId('app-password-home-only')).toBeNull()
  })

  it('«На этом устройстве» leads to /offline', async () => {
    vi.stubGlobal('caches', { open: vi.fn() })
    await open(adult)
    fireEvent.click(screen.getByRole('button', { name: /На этом устройстве/ }))
    expect(screen.getByTestId('where')).toHaveTextContent('/offline')
  })
})

describe('ProfileMenu', () => {
  it('leads to «Настройки» and holds no settings of its own', async () => {
    serve(adult)
    await session.login(adult.id)
    render(
      <MemoryRouter>
        <Menu />
        <Routes>
          <Route path="*" element={<Where />} />
        </Routes>
      </MemoryRouter>,
    )
    await userEvent.click(screen.getByRole('button', { name: /Мама/ }))
    for (const gone of [/Звук и субтитры/, /Уведомления на почту/, /Пароль для приложений/, /Устройства/, /О сервере/, /На этом устройстве/]) {
      expect(screen.queryByRole('menuitem', { name: gone })).toBeNull()
    }
    expect(screen.queryByRole('menuitemcheckbox')).toBeNull()
    expect(screen.queryByRole('menuitemradio', { name: 'English' })).toBeNull()
    expect(screen.getByRole('menuitem', { name: 'Сменить профиль' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('menuitem', { name: 'Настройки' }))
    expect(screen.getByTestId('where')).toHaveTextContent('/settings')
  })
})
