// «Задачи»: the background work in one place, read from /api/admin/tasks —
// the scan, the cleaned voice-overs and each worker with how much is done,
// whether it runs and why it stands still; a row opens its settings.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import type { TasksView } from '../../../api/types'
import { t } from '../../../i18n'
import { Tasks, workerLine } from '../Tasks'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

function Where() {
  return <span data-testid="where">{useLocation().pathname}</span>
}

const view: TasksView = {
  scan: { running: true, queued: false, movies: 0, episodes: 0, albums: 0, tracks: 0, books: 0, audiobooks: 0, comics: 0, files: 0, skipped: 0, removed: 0 },
  voclean: { done: 401, queued: 1138, failed: 0, notEligible: 57, backfill: true },
  workers: [
    { id: 'trickplay', running: false, done: 120, total: 3400, state: 'full' },
    { id: 'segments', running: true, done: 10, total: 40 },
    { id: 'loudness', running: false, done: 0, total: 0, lastRun: new Date().toISOString() },
  ],
}

afterEach(() => vi.unstubAllGlobals())

describe('«Задачи»', () => {
  it('shows every background job with its progress and opens its settings', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(view)))
    render(
      <MemoryRouter initialEntries={['/manage/tasks']}>
        <Routes>
          <Route path="/manage/tasks" element={<Tasks />} />
          <Route path="*" element={<Where />} />
        </Routes>
      </MemoryRouter>,
    )
    expect(await screen.findByText('Сканирование медиатеки')).toBeInTheDocument()
    expect(screen.getByText('Идёт сейчас')).toBeInTheDocument()
    expect(screen.getByText('Готово 401 · в очереди 1138 · ошибки 0 · не подходят 57 · вся медиатека')).toBeInTheDocument()
    expect(screen.getByText('Готово 120 из 3400 · место закончилось')).toBeInTheDocument()
    expect(screen.getByText('Готово 10 из 40 · идёт сейчас')).toBeInTheDocument()
    expect(screen.getByText(/^Делать нечего · последний проход/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /Кадры для перемотки/ }))
    expect(screen.getByTestId('where')).toHaveTextContent('/manage/settings/processing')
  })

  it('says what a long job is on, what it waits for and what failed', () => {
    const base = { id: 'transcripts', done: 2, total: 12 }
    expect(workerLine({ ...base, running: true, now: 'Старик и море', nowPercent: 40 }, t)).toBe('Готово 2 из 12 · идёт сейчас · «Старик и море»: 40\u00a0%')
    expect(workerLine({ ...base, running: false, state: 'waiting', error: 'stt: 429 Too Many Requests', lastRun: new Date().toISOString() }, t)).toMatch(
      /^Готово 2 из 12 · ждёт сервис · последний проход .+ · Ошибка: stt: 429 Too Many Requests$/,
    )
  })

  it('names the transcripts and opens the AI settings', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ workers: [{ id: 'transcripts', running: false, done: 1, total: 2 }] })))
    render(
      <MemoryRouter initialEntries={['/manage/tasks']}>
        <Routes>
          <Route path="/manage/tasks" element={<Tasks />} />
          <Route path="*" element={<Where />} />
        </Routes>
      </MemoryRouter>,
    )
    await userEvent.click(await screen.findByRole('button', { name: /Расшифровки аудиокниг/ }))
    expect(screen.getByTestId('where')).toHaveTextContent('/manage/settings/ai')
  })

  it('says a worker that never ran', () => {
    expect(workerLine({ id: 'x', running: false, done: 0, total: 0 }, t)).toBe('Ещё не запускалась')
  })
})
