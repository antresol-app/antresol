import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { SubtitleResult } from '../../api/types'
import { SubtitleSearchSheet, tracksNotRefreshed } from '../SubtitleSearchSheet'

const json = (b: unknown, status = 200) => new Response(b === undefined ? null : JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const ru: SubtitleResult[] = [
  { subtitleId: 11, lang: 'ru', release: 'Dune.Part.Two.2024.2160p.WEB-DL', downloads: 5321, hashMatch: true },
  { subtitleId: 12, lang: 'ru', release: 'Dune.Part.Two.2024.1080p.BluRay', downloads: 1204, hashMatch: false },
]
const en: SubtitleResult[] = [{ subtitleId: 21, lang: 'en', release: 'Dune Part Two SDH', downloads: 98, hashMatch: false }]

describe('SubtitleSearchSheet', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('SubtitleSearchSheet marks hash match and downloads', async () => {
    const posts: unknown[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        if (url.includes('/api/subtitles/5/search?lang=ru')) return json({ results: ru })
        if (url.includes('/api/subtitles/5/search?lang=en')) return json({ results: en })
        if (url.endsWith('/api/subtitles/5') && init?.method === 'POST') {
          posts.push(JSON.parse(String(init.body)))
          return json(undefined, 204)
        }
        return json({ error: 'нет' }, 404)
      }),
    )
    const onDone = vi.fn()
    render(<SubtitleSearchSheet fileId={5} onClose={() => {}} onDownloaded={onDone} />)
    const list = await screen.findByRole('list', { name: 'Найденные субтитры' })
    const rows = within(list).getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(within(rows[0]!).getByText('Dune.Part.Two.2024.2160p.WEB-DL')).toBeInTheDocument()
    expect(within(rows[0]!).getByText('Совпадает с файлом')).toBeInTheDocument()
    expect(within(rows[0]!).getByText(/5\s321 загрузка/)).toBeInTheDocument()
    expect(within(rows[1]!).queryByText('Совпадает с файлом')).not.toBeInTheDocument()

    await userEvent.click(within(rows[1]!).getByRole('button', { name: /Скачать/ }))
    await waitFor(() => expect(posts).toEqual([{ subtitleId: 12, lang: 'ru' }]))
    await waitFor(() => expect(onDone).toHaveBeenCalledWith('ru'))

    // English: another search
    await userEvent.click(screen.getByRole('radio', { name: 'Английские' }))
    expect(await screen.findByText('Dune Part Two SDH')).toBeInTheDocument()
    expect(screen.getByText('98 загрузок')).toBeInTheDocument()
  })

  it('keeps the sheet open with a message when the tracks are not refreshed', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        if (url.includes('/search?')) return json({ results: ru })
        if (init?.method === 'POST') return json(undefined, 204)
        return json({ error: 'нет' }, 404)
      }),
    )
    const onClose = vi.fn()
    render(<SubtitleSearchSheet fileId={5} onClose={onClose} onDownloaded={() => Promise.reject(new Error(tracksNotRefreshed()))} />)
    const rows = within(await screen.findByRole('list', { name: 'Найденные субтитры' })).getAllByRole('listitem')
    await userEvent.click(within(rows[0]!).getByRole('button', { name: /Скачать/ }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Субтитры скачаны, но список дорожек не обновился')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(onClose).not.toHaveBeenCalled()
  })

  it('says when the search is not set up', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => json({ error: 'Поиск субтитров не настроен: нужен ключ OpenSubtitles' }, 503)),
    )
    render(<SubtitleSearchSheet fileId={5} onClose={() => {}} onDownloaded={() => {}} />)
    expect(await screen.findByText('Поиск субтитров не настроен: нужен ключ OpenSubtitles')).toBeInTheDocument()
  })
})
