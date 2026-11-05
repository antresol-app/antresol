import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { MigrateReport, MigrateStatus } from '../../../api/types'
import { Migrate, summaryParts } from '../Migrate'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

const status: MigrateStatus = {
  sources: [
    { id: 'sonarr', name: 'Sonarr', found: true, path: '/import/sonarr/sonarr.db', detail: '15 сериалов, 15 отслеживается', error: '' },
    { id: 'kavita', name: 'Kavita', found: false, path: '/import/kavita/kavita.db', detail: 'Не найден — смонтируйте /import/kavita/kavita.db', error: '' },
    { id: 'jellyfin', name: 'Jellyfin', found: true, path: '/import/jellyfin/jellyfin.db', detail: '', error: '' },
  ],
  leftovers: [{ source: 'librarr', kind: 'leftover', name: 'Librarr', status: 'warning', reason: 'Не переносится: книги из списков Librarr (2) — добавьте вручную', ref: '', size: 0 }],
  users: [{ source: 'jellyfin', sourceId: '', sourceName: 'Jellyfin', vaultUserId: null, rows: 0, error: 'Не удалось прочитать базу Jellyfin' }],
  profiles: [{ id: 1, name: 'Александр' }],
  running: null,
  state: { switchedAt: null },
  pendingFollows: 0,
  libraryReady: { ok: false, reason: 'Медиатека ещё сканируется — подождите' },
}

afterEach(() => vi.restoreAllMocks())

describe('Migrate', () => {
  it('summarises a dry run and a real run', () => {
    expect(summaryParts({ counts: { planned: 4, warning: 1 }, dryRun: true }).map((p) => `${p.label} ${p.n}`)).toEqual([
      'Будет перенесено 4',
      'Пропущено 0',
      'Не получится 0',
      'Внимание 1',
    ])
    expect(summaryParts({ counts: { imported: 3, failed: 1 }, dryRun: false })[0]).toMatchObject({ label: 'Перенесено', n: 3 })
    expect(summaryParts({ counts: null, dryRun: false })[2]).toMatchObject({ label: 'Не получилось', n: 0 })
  })

  it('says what a rollback gave back, not «Перенесено»', () => {
    expect(summaryParts({ counts: { imported: 3, failed: 1 }, dryRun: false, rollbackOf: 'a' }).map((p) => `${p.label} ${p.n}`)).toEqual([
      'Возвращено 3',
      'Не получилось 1',
      'Внимание 0',
    ])
  })

  it('shows the cards, leftovers and the scan wait; nothing can run yet', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (u) => json(String(u).endsWith('/reports') ? [] : status))
    render(
      <MemoryRouter>
        <Migrate />
      </MemoryRouter>,
    )
    expect(await screen.findByText('Найден · 15 сериалов, 15 отслеживается')).toBeTruthy()
    expect(screen.getByText('Не найден — смонтируйте /import/kavita/kavita.db')).toBeTruthy()
    expect(screen.getByRole('checkbox', { name: /Kavita/ })).toHaveProperty('disabled', true)
    // an unreadable database is a problem on its card, not a user to map
    expect(screen.getByText('Не удалось прочитать базу Jellyfin')).toBeTruthy()
    expect(screen.queryByRole('list', { name: 'Профили' })).toBeNull()
    expect(screen.getByText('Не переносится: книги из списков Librarr (2) — добавьте вручную')).toBeTruthy()
    expect(screen.getByText('Медиатека ещё сканируется — подождите')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Проверить' })).toHaveProperty('disabled', true)
    expect(screen.getByRole('button', { name: 'Перенести' })).toHaveProperty('disabled', true)
  })

  const report = (r: Partial<MigrateReport>): MigrateReport => ({
    id: 'r',
    sources: ['jellyfin'],
    dryRun: false,
    planId: '',
    planHash: '',
    startedAt: '2026-10-25T19:30:00Z',
    finishedAt: '0001-01-01T00:00:00Z',
    counts: {},
    lines: [],
    ...r,
  })

  it('polls the light state during a run, not the old databases', async () => {
    const urls: string[] = []
    const running = report({ id: '20261025T193000Z-aaaaaa' })
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (u) => {
      const url = new URL(String(u), 'http://x').pathname
      urls.push(url)
      if (url.endsWith('/reports')) return json([running])
      if (url.includes('/reports/')) return json(running)
      const { sources: _s, leftovers: _l, users: _u, profiles: _p, ...st } = { ...status, running: running.id }
      if (url.endsWith('/state')) return json(st)
      return json({ ...status, running: running.id, libraryReady: { ok: true, reason: '' } })
    })
    render(
      <MemoryRouter>
        <Migrate />
      </MemoryRouter>,
    )
    await waitFor(() => expect(urls).toContain('/api/migrate/state'))
    expect(urls.filter((u) => u === '/api/migrate')).toHaveLength(1)
  })

  it('labels the lines and the history of a rollback «Возвращено»', async () => {
    const back = report({
      id: '20261025T203000Z-cccccc',
      rollbackOf: '20261025T193000Z-aaaaaa',
      finishedAt: '2026-10-25T20:31:00Z',
      counts: { imported: 1 },
      lines: [{ source: 'jellyfin', kind: 'progress', name: 'Дюна', status: 'imported', reason: 'Возвращено в Jellyfin', ref: 'ab', size: 0 }],
    })
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (u) => {
      const url = String(u)
      if (url.endsWith('/reports')) return json([back])
      if (url.includes('/reports/')) return json(back)
      return json({ ...status, libraryReady: { ok: true, reason: '' } })
    })
    render(
      <MemoryRouter>
        <Migrate />
      </MemoryRouter>,
    )
    const history = await screen.findByRole('list', { name: 'История переноса' })
    expect(within(history).getByText('Возвращено 1 · Не получилось 0 · Внимание 0')).toBeTruthy()
    within(history).getByRole('button', { name: 'Открыть' }).click()
    expect(await screen.findByLabelText('Возвращено')).toBeTruthy()
    expect(within(screen.getByTestId('migrate-summary')).queryByText(/Перенесено/)).toBeNull()
  })
})

// What the core build's server sends: the three media servers, and no
// `state` or `pendingFollows` (nothing to switch, nothing to give back).
const mediaOnly: MigrateStatus = {
  sources: [
    { id: 'jellyfin', name: 'Jellyfin', found: true, path: '/import/jellyfin/jellyfin.db', detail: '3 пользователя', error: '' },
    { id: 'kavita', name: 'Kavita', found: false, path: '/import/kavita/kavita.db', detail: 'Не найден — смонтируйте /import/kavita/kavita.db', error: '' },
    { id: 'audiobookshelf', name: 'Audiobookshelf', found: true, path: '/import/audiobookshelf/absdatabase.sqlite', detail: '2 пользователя', error: '' },
  ],
  leftovers: [],
  users: [],
  profiles: [{ id: 1, name: 'Александр' }],
  running: null,
  libraryReady: { ok: true, reason: '' },
}

describe('Migrate on a server with media servers only', () => {
  const mount = () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (u) => json(String(u).endsWith('/reports') ? [] : mediaOnly))
    render(
      <MemoryRouter>
        <Migrate />
      </MemoryRouter>,
    )
  }

  it('shows the three rows named by the server, with no switch', async () => {
    mount()
    expect(await screen.findByRole('checkbox', { name: /Jellyfin/ })).toBeTruthy()
    expect(screen.getByRole('checkbox', { name: /Kavita/ })).toHaveProperty('disabled', true)
    expect(screen.getByRole('checkbox', { name: /Audiobookshelf/ })).toBeTruthy()
    expect(screen.getByText('Просмотренное и «Избранное»')).toBeTruthy()
    expect(screen.queryByRole('button', { name: /Перейти на/ })).toBeNull()
    expect(screen.queryByTestId('migrate-switch-card')).toBeNull()
    // the neutral lead, not the sources build's
    expect(screen.getByText(/перенесёт прогресс и отметки из Jellyfin, Kavita и Audiobookshelf/)).toBeTruthy()
  })

  it.runIf(__LIBRARY_EDITION__)('core build: media servers only', async () => {
    mount()
    expect(await screen.findByRole('checkbox', { name: /Jellyfin/ })).toBeTruthy()
    // let anything that could still load settle
    await new Promise((r) => setTimeout(r, 50))
    expect(screen.getAllByTestId('migrate-source').map((li) => li.textContent)).toEqual([
      expect.stringContaining('Jellyfin'),
      expect.stringContaining('Kavita'),
      expect.stringContaining('Audiobookshelf'),
    ])
    expect(document.body.textContent).not.toMatch(/Sonarr|Radarr|[Рр]аздач/)
    expect(screen.queryByRole('button', { name: /Перейти на/ })).toBeNull()
  })
})
