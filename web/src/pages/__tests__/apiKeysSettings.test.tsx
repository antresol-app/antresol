// «Ключи API и ИИ» (pages/ApiKeysSettings.tsx): the list, a key created and
// shown once with the ways to connect it, a revoke, and what a kid gets.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import type { ApiKey, User } from '../../api/types'
import { session } from '../../state/session'
import { ApiKeysSettings } from '../ApiKeysSettings'
import { ProfileSettings } from '../ProfileSettings'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const adult: User = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: true, isAdmin: false, isKid: false, maxRating: null }
const kid: User = { ...adult, id: 3, name: 'Вася', isKid: true, maxRating: '12+' }
const SECRET = 'antresol_test-only-placeholder'

function serve(user: User, publicUrl = '') {
  let keys: ApiKey[] = [{ id: 1, name: 'Скрипт', scope: 'read', hint: 'ab12', createdAt: '2026-11-01T10:00:00Z', lastUsedAt: null }]
  const calls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      calls.push(`${method} ${url}`)
      if (url.endsWith('/api/login')) return json(user)
      if (url === '/api/me/api-keys' && method === 'GET') return json({ keys, canAdmin: user.isAdmin, publicUrl, mcpPath: '/mcp' })
      if (url === '/api/me/api-keys' && method === 'POST') {
        const body = JSON.parse(String(init?.body))
        const info: ApiKey = { id: 2, name: body.name, scope: body.scope, hint: 'zz99', createdAt: new Date().toISOString(), lastUsedAt: null }
        keys = [info, ...keys]
        return json({ key: SECRET, info })
      }
      if (url.startsWith('/api/me/api-keys/') && method === 'DELETE') {
        keys = keys.filter((k) => `/api/me/api-keys/${k.id}` !== url)
        return new Response(null, { status: 204 })
      }
      if (url.startsWith('/api/me/api-keys/log')) return json({ events: [] })
      return json({})
    }),
  )
  return calls
}

function Where() {
  const l = useLocation()
  return <span data-testid="where">{l.pathname}</span>
}

async function open(user: User, publicUrl = '', at = '/settings/api-keys') {
  const calls = serve(user, publicUrl)
  await session.login(user.id)
  render(
    <MemoryRouter initialEntries={[at]}>
      <Routes>
        <Route path="/settings/api-keys" element={<ApiKeysSettings />} />
        <Route path="/settings" element={<ProfileSettings />} />
        <Route path="*" element={<Where />} />
      </Routes>
    </MemoryRouter>,
  )
  return calls
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('ApiKeysSettings', () => {
  it('creates a key, shows it once with the Claude Code command, and revokes one', async () => {
    const calls = await open(adult, 'https://media.example.org')
    expect(await screen.findByText('Скрипт')).toBeInTheDocument()
    expect(screen.getByText(/Только чтение · создан/)).toBeInTheDocument()
    // the connect panel uses the public address and a placeholder
    expect(screen.getAllByDisplayValue(/claude mcp add --transport http antresol https:\/\/media\.example\.org\/mcp --header "Authorization: Bearer ВАШ_КЛЮЧ"/)).toHaveLength(1)

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Создать ключ' }))
    const sheet = await screen.findByRole('dialog', { name: 'Новый ключ' })
    await user.type(within(sheet).getByLabelText('Название'), 'Claude')
    await user.click(within(sheet).getByRole('button', { name: 'Создать' }))
    const shown = await screen.findByRole('dialog', { name: 'Ключ «Claude» создан' })
    expect(within(shown).getByDisplayValue(SECRET)).toBeInTheDocument()
    expect(within(shown).getByDisplayValue(`claude mcp add --transport http antresol https://media.example.org/mcp --header "Authorization: Bearer ${SECRET}"`)).toBeInTheDocument()
    expect(calls).toContain('POST /api/me/api-keys')
    await user.click(within(shown).getByRole('button', { name: 'Готово' }))
    // never shown again
    await waitFor(() => expect(screen.queryByDisplayValue(SECRET)).not.toBeInTheDocument())

    await user.click(await screen.findByRole('button', { name: 'Отозвать: Скрипт' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Отозвать ключ «Скрипт»?' })
    await user.click(within(confirm).getByRole('button', { name: 'Отозвать' }))
    await waitFor(() => expect(calls).toContain('DELETE /api/me/api-keys/1'))
    await waitFor(() => expect(screen.queryByText('Скрипт')).not.toBeInTheDocument())
  })

  it('offers administration only to an admin', async () => {
    await open(adult)
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Создать ключ' }))
    const sheet = await screen.findByRole('dialog', { name: 'Новый ключ' })
    await user.click(within(sheet).getByRole('button', { name: /Что можно/ }))
    expect(await screen.findByRole('menuitemradio', { name: /Чтение и действия/ })).toBeInTheDocument()
    expect(screen.queryByRole('menuitemradio', { name: /Администрирование/ })).not.toBeInTheDocument()
  })

  it('is reached from «Мои настройки» and not by a kid', async () => {
    await open(adult, '', '/settings')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /Ключи API и ИИ/ }))
    expect(await screen.findByRole('heading', { name: 'Ключи API и ИИ' })).toBeInTheDocument()
    cleanup()
    await open(kid)
    // a kid is sent back to its settings, which have no such row
    expect(await screen.findByRole('heading', { name: 'Настройки' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Ключи API и ИИ/ })).not.toBeInTheDocument()
  })
})
