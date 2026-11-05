// VAULT_LIBRARY_ONLY (me.libraryOnly): the screens whose API answers 404
// on such a server are not offered.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import type { User } from '../../api/types'
import { session } from '../../state/session'
import { Manage } from '../manage/Manage'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const adult: User = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: true, isAdmin: false, isKid: false, maxRating: null }
const admin: User = { ...adult, id: 1, name: 'Папа', isAdmin: true }

function Where() {
  return <span data-testid="where">{useLocation().pathname}</span>
}

async function signIn(u: User) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => (String(input).endsWith('/api/login') ? json(u) : json({ error: 'нет' }, 404))),
  )
  await session.login(u.id)
}

function renderManage() {
  render(
    <MemoryRouter initialEntries={['/manage']}>
      <Routes>
        <Route path="/manage" element={<Manage />}>
          <Route path="*" element={<Where />} />
        </Route>
      </Routes>
    </MemoryRouter>,
  )
}

beforeEach(() => session.reset())
afterEach(() => {
  vi.unstubAllGlobals()
  session.reset()
})

describe('library-only server', () => {
  it('«Управление» hides the sections the server lacks and opens on one it has', async () => {
    await signIn({ ...admin, libraryOnly: true })
    renderManage()
    expect(await screen.findByTestId('where')).toHaveTextContent('/manage/files')
    for (const name of [/Загрузки/, /Слежение/, /Источники/]) {
      expect(screen.queryByRole('link', { name })).toBeNull()
    }
    expect(screen.getByRole('link', { name: /Файлы/ })).toBeInTheDocument()
    // moving the media servers' progress is in every edition, and is not where «Управление» opens
    expect(screen.getByRole('link', { name: /Переезд/ })).toBeInTheDocument()
    // so is the playlist import from files (it settles by the library alone), not where it opens either
    expect(screen.getByRole('link', { name: /Импорт/ })).toBeInTheDocument()
  })
})
