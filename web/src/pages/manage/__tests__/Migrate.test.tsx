import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { MigrateReport, MigrateStatus } from '../../../api/types'
import { registerAddon, resetAddonsForTests, setAddonUser } from '../../../addons/registry'
import { session } from '../../../state/session'
import { Migrate, summaryParts, type MigrateApi, type MigrateFullApi } from '../Migrate'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

const status: MigrateStatus = {
  sources: [
    { id: 'oldshows', name: 'OldShows', found: true, path: '/import/oldshows/oldshows.db', detail: '15 сериалов, 15 отслеживается', error: '' },
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
    // the core's own title of a rollback (an addon's part names its own)
    expect(within(history).getByText(/^Возврат перенесённого ·/)).toBeTruthy()
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

  it('without an addon: media servers only', async () => {
    mount()
    expect(await screen.findByRole('checkbox', { name: /Jellyfin/ })).toBeTruthy()
    // let anything that could still load settle
    await new Promise((r) => setTimeout(r, 50))
    expect(screen.getAllByTestId('migrate-source').map((li) => li.textContent)).toEqual([
      expect.stringContaining('Jellyfin'),
      expect.stringContaining('Kavita'),
      expect.stringContaining('Audiobookshelf'),
    ])
    expect(document.body.textContent).not.toMatch(/OldShows/)
    expect(screen.queryByRole('button', { name: /Перейти на/ })).toBeNull()
  })
})

const admin = { id: 1, name: 'Папа', avatar: 'grad-3', hasPassword: false, isAdmin: true, isKid: false, maxRating: null }

async function signInAdmin() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => json(admin)),
  )
  await session.login(admin.id)
  vi.unstubAllGlobals()
  act(() => setAddonUser({ isAdmin: true }))
}

describe('Migrate with addons', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    session.reset()
    resetAddonsForTests()
  })

  it('renders addon source cards', async () => {
    await signInAdmin()
    registerAddon('alpha', {
      migrateSources: [{ id: 'old', name: 'src.name', icon: 'arrowRight', to: 'migrate', admin: true, summary: () => <span>Ждут переноса: 3</span> }],
      strings: { ru: { 'src.name': 'Старые службы' }, en: { 'src.name': 'Old services' } },
    })
    registerAddon('beta', { migrateSources: [{ id: 'other', name: 'other.name', icon: 'plug', to: '/manage/import' }] })
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (u) => json(String(u).endsWith('/reports') ? [] : mediaOnly))
    render(
      <MemoryRouter>
        <Migrate />
      </MemoryRouter>,
    )
    const cards = await screen.findByRole('list', { name: 'Другие переезды' })
    const links = within(cards).getAllByRole('link')
    // the name is a strings key of the addon; a key it lacks shows as itself
    expect(links.map((a) => [a.textContent, a.getAttribute('href')])).toEqual([
      ['Старые службыЖдут переноса: 3', '/addons/alpha/migrate'],
      ['other.name', '/manage/import'],
    ])
    // the core's own services are still checkboxes of this page
    expect(screen.getByRole('checkbox', { name: /Jellyfin/ })).toBeTruthy()
  })

  it('shows no addon cards without one, nor on an addon\'s own page', async () => {
    await signInAdmin()
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (u) => json(String(u).endsWith('/reports') ? [] : mediaOnly))
    const { unmount } = render(
      <MemoryRouter>
        <Migrate />
      </MemoryRouter>,
    )
    expect(await screen.findByRole('checkbox', { name: /Jellyfin/ })).toBeTruthy()
    expect(screen.queryByRole('list', { name: 'Другие переезды' })).toBeNull()
    unmount()
    registerAddon('alpha', { migrateSources: [{ id: 'old', name: 'src.name', icon: 'arrowRight', to: 'migrate' }] })
    const api: MigrateApi = {
      migrateStatus: async () => mediaOnly,
      migrateState: async () => mediaOnly,
      migrateRun: async () => ({ reportId: 'r' }),
      migrateReports: async () => [],
      migrateReport: async () => report({}),
      useProgress: () => {},
    }
    render(
      <MemoryRouter>
        <Migrate api={api} />
      </MemoryRouter>,
    )
    expect(await screen.findByRole('checkbox', { name: /Jellyfin/ })).toBeTruthy()
    expect(screen.queryByRole('list', { name: 'Другие переезды' })).toBeNull()
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

  it('uses an injected api and progress source', async () => {
    const fetch = vi.spyOn(globalThis, 'fetch')
    const running = report({ id: 'rep-1', lines: [] })
    let lines: MigrateReport['lines'] = []
    let progress: ((data: unknown) => void) | null = null
    const asked: string[] = []
    const api: MigrateApi = {
      migrateStatus: async () => {
        asked.push('status')
        return { ...mediaOnly, running: 'rep-1' }
      },
      migrateState: async () => {
        asked.push('state')
        return { running: 'rep-1', libraryReady: { ok: true, reason: '' } }
      },
      migrateRun: async () => ({ reportId: 'rep-2' }),
      migrateReports: async () => {
        asked.push('reports')
        return []
      },
      migrateReport: async (id) => {
        asked.push(`report ${id}`)
        return { ...running, lines }
      },
      useProgress: (h) => {
        progress = h
      },
    }
    render(
      <MemoryRouter>
        <Migrate api={api} />
      </MemoryRouter>,
    )
    expect(await screen.findByTestId('migrate-report')).toBeTruthy()
    expect(asked).toContain('status')
    expect(asked).toContain('reports')
    expect(asked).toContain('report rep-1')
    lines = [{ source: 'jellyfin', kind: 'progress', name: 'Дюна', status: 'imported', reason: '', ref: '', size: 0 }]
    // a progress event of another report is not this one's
    const before = asked.filter((a) => a === 'report rep-1').length
    act(() => progress!({ reportId: 'other' }))
    act(() => progress!({ reportId: 'rep-1' }))
    expect(await screen.findByText('Дюна')).toBeTruthy()
    expect(asked.filter((a) => a === 'report rep-1').length).toBeGreaterThan(before)
    // nothing went to the core's own /api/migrate
    expect(fetch.mock.calls.map((c) => String(c[0])).filter((u) => u.includes('/api/migrate'))).toEqual([])
  })

  it('keeps an addon page\'s own subtitle once loaded, also when its status has no state', async () => {
    const api: MigrateApi = {
      migrateStatus: async () => mediaOnly,
      migrateState: async () => mediaOnly,
      migrateRun: async () => ({ reportId: 'r' }),
      migrateReports: async () => [],
      migrateReport: async () => report({}),
      useProgress: () => {},
    }
    const None = () => null
    const full: MigrateFullApi = { source: () => undefined, TopCards: None, HistoryAction: None, ConfirmItems: None, progressNote: () => '', subtitle: () => 'Своя подпись.' }
    render(
      <MemoryRouter>
        <Migrate api={api} full={full} />
      </MemoryRouter>,
    )
    expect(screen.getByText(/^Своя подпись\./)).toBeTruthy() // loading
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    expect(screen.getByText(/^Своя подпись\./)).toBeTruthy() // loaded: no flip to the core's
  })

  it('titles a rollback with the addon part\'s own text', async () => {
    const back = report({ id: 'back', rollbackOf: 'r0', finishedAt: '2026-10-25T20:31:00Z', counts: { imported: 1 } })
    const api: MigrateApi = {
      migrateStatus: async () => ({ ...mediaOnly, state: { switchedAt: null } }),
      migrateState: async () => ({ ...mediaOnly, state: { switchedAt: null } }),
      migrateRun: async () => ({ reportId: 'r' }),
      migrateReports: async () => [back],
      migrateReport: async () => back,
      useProgress: () => {},
    }
    const None = () => null
    const full: MigrateFullApi = { source: () => undefined, TopCards: None, HistoryAction: None, ConfirmItems: None, progressNote: () => '', subtitle: () => 'Своя подпись.', rollbackTitle: () => 'Свой возврат' }
    render(
      <MemoryRouter>
        <Migrate api={api} full={full} />
      </MemoryRouter>,
    )
    const history = await screen.findByRole('list', { name: 'История переноса' })
    expect(within(history).getByText(/^Свой возврат ·/)).toBeTruthy()
  })

  it('draws an addon part\'s SourceSetup under each card, outside its checkbox, with the page\'s context', async () => {
    const withOld = { ...mediaOnly, state: { switchedAt: null }, sources: [...mediaOnly.sources, { id: 'oldtool', name: 'OldTool', found: false, path: '/import/oldtool', detail: 'Не подключён', error: '' }] }
    const api: MigrateApi = {
      migrateStatus: async () => withOld,
      migrateState: async () => withOld,
      migrateRun: async () => ({ reportId: 'r' }),
      migrateReports: async () => [],
      migrateReport: async () => report({}),
      useProgress: () => {},
    }
    const None = () => null
    const seen: string[] = []
    const full: MigrateFullApi = {
      source: () => undefined,
      TopCards: None,
      HistoryAction: None,
      ConfirmItems: None,
      progressNote: () => '',
      subtitle: () => 'Своя подпись.',
      // draws for its own service only; the card of another one stays alone
      SourceSetup: ({ ctx, source }) => {
        seen.push(`${source.id}:${ctx.status.sources.length}`)
        return source.id === 'oldtool' ? <button type="button">Подключить OldTool</button> : null
      },
    }
    render(
      <MemoryRouter>
        <Migrate api={api} full={full} />
      </MemoryRouter>,
    )
    const connect = await screen.findByRole('button', { name: 'Подключить OldTool' })
    // under the card, not inside the label of its checkbox
    expect(connect.closest('label')).toBeNull()
    expect(connect.closest('[data-testid="migrate-source"]')).toBeTruthy()
    expect(screen.getAllByTestId('migrate-source')).toHaveLength(withOld.sources.length)
    expect(seen).toContain(`oldtool:${withOld.sources.length}`)
    expect(screen.getByRole('checkbox', { name: /OldTool/ })).toHaveProperty('disabled', true)
  })

  it('draws an addon part\'s own texts (functions) beside core keys', async () => {
    const withOld = { ...mediaOnly, state: { switchedAt: null }, sources: [...mediaOnly.sources, { id: 'oldtool', name: 'OldTool', found: true, path: '/import/oldtool', detail: '2', error: '' }] }
    const api: MigrateApi = {
      migrateStatus: async () => withOld,
      migrateState: async () => withOld,
      migrateRun: async () => ({ reportId: 'r' }),
      migrateReports: async () => [],
      migrateReport: async () => report({}),
      useProgress: () => {},
    }
    const None = () => null
    const full: MigrateFullApi = {
      source: (id) => (id === 'oldtool' ? { icon: 'plug', what: () => 'Своё: что придёт', note: () => 'Своя заметка' } : undefined),
      TopCards: None,
      HistoryAction: None,
      ConfirmItems: None,
      progressNote: () => '',
      subtitle: () => 'Своя подпись.',
    }
    render(
      <MemoryRouter>
        <Migrate api={api} full={full} />
      </MemoryRouter>,
    )
    expect(await screen.findByText('Своё: что придёт')).toBeTruthy()
    expect(screen.getByText('Своя заметка')).toBeTruthy()
    expect(screen.getByText(/^Своя подпись\./)).toBeTruthy()
    // a core key still resolves from the core's catalog
    expect(screen.getByText('Просмотренное и «Избранное»')).toBeTruthy()
  })
})
