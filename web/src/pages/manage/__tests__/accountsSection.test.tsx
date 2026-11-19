// «Аккаунты и синхронизация» in «Настройки сервера»: one row of «Управление»'s
// list for what applies to the whole server (Shikimori's application), under
// the same name as the profile's page, and no longer a section of the library's
// settings.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import type { Settings as Values } from '../../../api/types'
import { t } from '../../../i18n'
import { MANAGE_SECTIONS } from '../Manage'
import { Settings, settingsGroups } from '../Settings'
import { renderSettings } from './settingsHarness'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const base: Values = { quality: 1080, voice: 'dub', remoteQuality: '1080', remoteMusic: 'high', autoSubtitles: true, subtitleLangs: ['ru', 'en'], subtitlesAvailable: false }

function stub(admin: unknown = { clientId: 'app-id', hasSecret: true, fromEnv: false, redirectUri: 'https://media.example/api/accounts/shikimori/callback', scope: 'user_rates' }) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/accounts/admin/shikimori')) return admin ? json(admin) : json({ error: 'нет' }, 404)
      if (url.endsWith('/api/settings')) return json(base)
      if (url.endsWith('/api/library/status')) return json({ running: false, queued: false, movies: 0, episodes: 0 })
      return json({})
    }),
  )
}

afterEach(() => vi.unstubAllGlobals())

describe('«Управление»: the accounts row', () => {
  it('is one row of the server part, named as the profile’s page, and leads to a group that exists', () => {
    const rows = MANAGE_SECTIONS.filter((s) => s.to === 'settings/accounts')
    expect(rows).toHaveLength(1)
    expect(rows[0]!.group).toBe('server')
    expect(t(rows[0]!.label)).toBe('Аккаунты и синхронизация')
    // every «settings/<group>» row has its screen
    const groups = settingsGroups().map((g) => g.id as string)
    for (const s of MANAGE_SECTIONS.filter((s) => s.to.startsWith('settings/'))) expect(groups, s.to).toContain(s.to.slice('settings/'.length))
  })
})

describe('Настройки сервера: Аккаунты и синхронизация', () => {
  it('holds Shikimori’s application: its Client ID, the secret and the address to enter there', async () => {
    stub()
    renderSettings('accounts')
    expect(await screen.findByRole('heading', { name: 'Аккаунты и синхронизация', level: 2 })).toBeInTheDocument()
    expect(await screen.findByLabelText('Client ID')).toHaveValue('app-id')
    expect(screen.getByLabelText('Client Secret')).toHaveAttribute('placeholder', 'сохранён')
    expect(screen.getByDisplayValue('https://media.example/api/accounts/shikimori/callback')).toHaveAttribute('readonly')
  })

  it('is no longer under «Медиатека»', async () => {
    stub()
    renderSettings('library')
    expect(await screen.findByRole('heading', { name: 'Медиатека', level: 2 })).toBeInTheDocument()
    await waitFor(() => expect(fetch).toHaveBeenCalledWith('/api/settings', expect.anything()))
    expect(screen.queryByLabelText('Client ID')).toBeNull()
    expect(screen.queryByText('Приложение Shikimori')).toBeNull()
  })

  it('leads to the administrator’s own accounts, which are a profile’s settings', async () => {
    stub()
    function Where() {
      return <span data-testid="where">{useLocation().pathname}</span>
    }
    render(
      <MemoryRouter initialEntries={['/manage/settings/accounts']}>
        <Routes>
          <Route path="/manage/settings/:group?" element={<Settings />} />
          <Route path="*" element={<Where />} />
        </Routes>
      </MemoryRouter>,
    )
    await userEvent.click(await screen.findByRole('button', { name: /Мои аккаунты/ }))
    expect(screen.getByTestId('where')).toHaveTextContent('/settings/accounts')
  })

  it('still has the way to the own accounts where the server has no accounts at all', async () => {
    stub(null)
    renderSettings('accounts')
    expect(await screen.findByRole('button', { name: /Мои аккаунты/ })).toBeInTheDocument()
    expect(screen.queryByLabelText('Client ID')).toBeNull()
  })
})
