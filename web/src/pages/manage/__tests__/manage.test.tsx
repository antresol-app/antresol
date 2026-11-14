import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { Files } from '../Files'
import { renderSettings } from './settingsHarness'

function json(body: unknown, status = 200) {
  return new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

type Handler = (url: string, init?: RequestInit) => Response | undefined
function stubFetch(h: Handler) {
  const f = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => h(String(input), init) ?? json({ error: 'нет' }, 404))
  vi.stubGlobal('fetch', f)
  return f
}

afterEach(() => vi.unstubAllGlobals())

describe('Files storage panel', () => {
  it('ends in an error with «Повторить» when the disk cannot be measured', async () => {
    let fail = true
    stubFetch((u) => {
      if (u.endsWith('/api/files/usage')) return fail ? json({ error: 'нет' }, 500) : json({ total: 1000, used: 400, free: 600, categories: [{ id: 'movies', bytes: 400 }] })
      if (u.endsWith('/api/stats/storage')) return json({ items: [] })
      return undefined
    })
    render(
      <MemoryRouter>
        <Files />
      </MemoryRouter>,
    )
    expect(await screen.findByText('Не удалось посчитать место')).toBeInTheDocument()
    expect(screen.queryByText('Считаем место…')).not.toBeInTheDocument()
    fail = false
    await userEvent.click(screen.getByRole('button', { name: 'Повторить' }))
    expect(await screen.findByText(/Свободно 600 Б из/)).toBeInTheDocument()
  })

  it('no disk figures (a library-only server): no meter and no endless «Считаем место…»', async () => {
    const usage = vi.fn(() => json({ total: 0, used: 0, free: 0, categories: [{ id: 'movies', bytes: 400 }] }))
    stubFetch((u) => {
      if (u.endsWith('/api/files/usage')) return usage()
      if (u.endsWith('/api/stats/storage')) return json({ items: [] })
      return undefined
    })
    render(
      <MemoryRouter>
        <Files />
      </MemoryRouter>,
    )
    await waitFor(() => expect(usage).toHaveBeenCalled())
    await waitFor(() => expect(screen.queryByText('Считаем место…')).not.toBeInTheDocument())
    expect(screen.queryByText('Медиадиск')).not.toBeInTheDocument()
  })
})

describe('Settings: «Качество вне дома»', () => {
  it('shows 1080p by default and saves another choice', async () => {
    const values = { quality: 1080, voice: 'dub', maxSizeGB: 0, uploadLimited: false, uploadLimitKBs: 0, streamCacheGB: 20, remoteQuality: '1080' }
    const puts: unknown[] = []
    stubFetch((url, init) => {
      if (url === '/api/settings' && init?.method === 'PUT') {
        const body = JSON.parse(String(init.body))
        puts.push(body)
        return json(body)
      }
      if (url === '/api/settings') return json(values)
      if (url === '/api/library/status') return json({ running: false, queued: false, movies: 0, episodes: 0 })
      return undefined
    })
    renderSettings('watching')
    const group = await screen.findByRole('radiogroup', { name: 'Качество вне дома' })
    expect(within(group).getByRole('radio', { name: '1080p' })).toHaveAttribute('aria-checked', 'true')
    await userEvent.click(within(group).getByRole('radio', { name: '720p' }))
    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0]).toMatchObject({ remoteQuality: '720', quality: 1080 })
  })
})
