// A title in the library: «Смотреть» always plays the server's file, ▾ adds
// «Онлайн» (another build only), and the round download button saves the film to
// this device — one file is a plain download link, several open the version
// menu with the file's real tracks.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { downloadUrl } from '../../api/client'
import type { ItemDetail as Detail, MediaFile, SubTrack, Track } from '../../api/types'
import { ItemDetail } from '../../pages/ItemDetail'
import { session } from '../../state/session'
import { ActionBar } from '../ActionBar'
import { setViewport } from '../../test/setup'

const audio = (lang: string): Track => ({ index: 1, codec: 'aac', lang, title: '', default: false })
const sub = (lang: string): SubTrack => ({ index: 2, codec: 'srt', lang, title: '', forced: false, default: false, external: false, burnIn: false })
const file = (id: number, over: Partial<MediaFile> = {}): MediaFile => ({ id, name: `dune-${id}.mkv`, size: 4 * 2 ** 30, audio: [], subs: [], ...over })

function film(files: MediaFile[], over: Partial<Detail> = {}): Detail {
  return {
    id: 9, kind: 'movie', title: 'Дюна', subtitle: '', poster: '', backdrop: '', logo: '', progress: 0, onServer: true,
    overview: '', genres: [], cast: [], seasons: [], files, inWatchlist: false,
    actions: { play: true, toServer: false, toDevice: true, watchNow: false }, ...over,
  }
}

describe('ActionBar on a library title', () => {
  it('one file: the round button is a download link, never a ring', () => {
    render(<ActionBar item={film([file(5)])} onPlay={() => {}} />)
    const link = screen.getByRole('link', { name: 'Загрузить' })
    expect(link).toHaveAttribute('href', downloadUrl(5))
    expect(link).toHaveAttribute('download')
    expect(screen.queryByRole('progressbar')).toBeNull()
    expect(screen.queryByText('На это устройство')).toBeNull()
  })

  it('two versions: the menu rows carry quality, audio, subtitles and size', async () => {
    const files = [
      file(5, { resolution: '2160p', audio: [audio('rus'), audio('eng')], subs: [sub('eng')] }),
      file(6, { resolution: '1080p', size: 2 * 2 ** 30, audio: [audio('ru')] }),
    ]
    render(<ActionBar item={film(files)} onPlay={() => {}} />)
    await userEvent.click(screen.getByRole('button', { name: 'Загрузить' }))
    const menu = screen.getByRole('menu', { name: 'Скачать на это устройство' })
    const rows = within(menu).getAllByRole('menuitem')
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveAttribute('href', downloadUrl(5))
    expect(rows[0]).toHaveAttribute('download')
    // the label already says «Качество 2160p»: the detail does not repeat it
    expect(rows[0]).toHaveTextContent('Качество 2160p')
    expect(rows[0]).toHaveTextContent('RU, EN · Субтитры: EN · 4,0 ГБ')
    expect(rows[0]!.textContent?.match(/2160p/g)).toHaveLength(1)
    expect(rows[1]).toHaveTextContent('RU · 2,0 ГБ')
    expect(rows[1]!.textContent?.match(/1080p/g)).toHaveLength(1)
  })

  it('«Ещё» lists the versions to watch with audio, subtitles and size, not the quality twice', async () => {
    const files = [
      file(5, { resolution: '2160p', audio: [audio('rus'), audio('eng')], subs: [sub('eng')] }),
      file(6, { resolution: '1080p', size: 2 * 2 ** 30, audio: [audio('ru')] }),
    ]
    const onPlay = vi.fn()
    render(<ActionBar item={film(files)} onPlay={onPlay} />)
    await userEvent.click(screen.getByRole('button', { name: 'Ещё' }))
    const row = screen.getByRole('menuitem', { name: /Смотреть в 2160p/ })
    expect(row).toHaveTextContent('RU, EN · Субтитры: EN · 4,0 ГБ')
    expect(row.textContent?.match(/2160p/g)).toHaveLength(1)
    expect(screen.getByRole('menuitem', { name: /Смотреть в 1080p/ })).toHaveTextContent('RU · 2,0 ГБ')
    await userEvent.click(row)
    expect(onPlay).toHaveBeenCalledWith({ fileId: 5 })
  })

  it('parts of one film keep their resolution in the detail', async () => {
    const files = [
      file(5, { name: 'dune-cd1.mkv', resolution: '1080p', audio: [audio('ru')] }),
      file(6, { name: 'dune-cd2.mkv', resolution: '1080p', audio: [audio('ru')] }),
    ]
    render(<ActionBar item={film(files)} onPlay={() => {}} />)
    await userEvent.click(screen.getByRole('button', { name: 'Загрузить' }))
    const rows = within(screen.getByRole('menu', { name: 'Скачать на это устройство' })).getAllByRole('menuitem')
    expect(rows[0]).toHaveTextContent('Часть 1')
    expect(rows[0]).toHaveTextContent('1080p · RU · 4,0 ГБ')
  })

  it('nothing to download as a file: no download button', () => {
    render(<ActionBar item={film([], { kind: 'series' })} onPlay={() => {}} />)
    expect(screen.queryByRole('button', { name: 'Загрузить' })).toBeNull()
    expect(screen.queryByRole('link', { name: 'Загрузить' })).toBeNull()
  })

  it('with another way the ▾ menu has «С сервера» checked, and the main part plays the server file', async () => {
    const onPlay = vi.fn()
    const online = vi.fn()
    render(<ActionBar item={film([file(5)])} onPlay={onPlay} moreWays={[{ id: 'online', label: 'Онлайн', run: online }]} />)
    await userEvent.click(screen.getByRole('button', { name: 'Смотреть' }))
    expect(onPlay).toHaveBeenCalledTimes(1)
    await userEvent.click(screen.getByRole('button', { name: 'Другие способы просмотра' }))
    expect(screen.getByRole('menuitemradio', { name: /С сервера/ })).toHaveAttribute('aria-checked', 'true')
    await userEvent.click(screen.getByRole('menuitemradio', { name: /Онлайн/ }))
    expect(online).toHaveBeenCalledTimes(1)
    // the server way stays the main part's: a library item never remembers another
    await userEvent.click(screen.getByRole('button', { name: 'Смотреть' }))
    expect(onPlay).toHaveBeenCalledTimes(2)
  })
})

// The page wires the online way only in the sources build.
const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const user = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null, canWatchOnline: true }

async function openPage() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/login')) return json(user)
      if (url.includes('/api/items/9')) return json(film([file(5)], { tmdbId: 42 }))
      return json({})
    }),
  )
  await session.login(user.id)
  render(
    <MemoryRouter initialEntries={['/item/9']}>
      <Routes>
        <Route path="/item/:id" element={<ItemDetail />} />
      </Routes>
    </MemoryRouter>,
  )
  await screen.findByRole('heading', { name: 'Дюна' })
}

describe('library page', () => {
  beforeEach(() => session.reset())
  afterEach(() => {
    vi.unstubAllGlobals()
    session.reset()
  })

  it.runIf(__LIBRARY_EDITION__)('core build: a plain «Смотреть», no ▾ and no «Онлайн»', async () => {
    await openPage()
    expect(screen.getByRole('button', { name: 'Смотреть' })).not.toHaveAttribute('aria-haspopup')
    expect(screen.queryByRole('button', { name: 'Другие способы просмотра' })).toBeNull()
    expect(document.body.textContent).not.toMatch(/Онлайн/)
    expect(screen.getByRole('link', { name: 'Загрузить' })).toHaveAttribute('href', downloadUrl(5))
  })
})

// The row's round buttons: «Хочу посмотреть» is a circle like its neighbours,
// and every round toggle that is on (it, «Следить») shares one look — white,
// keeping its icon.
describe('round toggles of the action row', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('«Хочу посмотреть» is a round icon button that turns white and keeps its icon', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 204 })))
    render(<ActionBar item={film([file(5)])} onPlay={() => {}} />)
    const wl = screen.getByRole('button', { name: 'Хочу посмотреть' })
    expect(wl).toHaveClass('button', 'lg', 'glass')
    expect(wl).toHaveAttribute('aria-pressed', 'false')
    expect(wl).toHaveAttribute('title', 'Добавить в «Хочу посмотреть»')
    expect(wl.querySelector('svg')?.getAttribute('data-icon')).toBe('watchlist')
    await userEvent.click(wl)
    expect(wl).toHaveAttribute('aria-pressed', 'true')
    expect(wl).toHaveClass('on')
    expect(wl).toHaveAttribute('title', 'В списке «Хочу посмотреть»')
    expect(wl.querySelector('svg')?.getAttribute('data-icon')).toBe('watchlist')
  })

  it('the watch progress sits under «Продолжить», in the same column', () => {
    const f = file(5, { durationMs: 100 * 60_000 })
    render(<ActionBar item={film([f], { progress: 0.5 })} onPlay={() => {}} />)
    const bar = screen.getByRole('progressbar', { name: 'Просмотрено' })
    const column = bar.closest('.play')
    expect(column).not.toBeNull()
    expect(within(column as HTMLElement).getByRole('button', { name: /Продолжить/ })).toBeInTheDocument()
  })

  it('wide screen: the admin’s «Удалить» is the red round button at the end of the row', () => {
    render(<ActionBar item={film([file(5)])} onPlay={() => {}} deletion={{ itemId: 9, title: 'Дюна', onDeleted: () => {} }} />)
    const trash = screen.getByRole('button', { name: 'Удалить' })
    expect(trash).toHaveClass('destructive')
    expect(trash.closest('[data-action-tiles]')).not.toBeNull()
  })

  it('phone: «Удалить…» is the last item of «Ещё», not a tile, and opens the confirmation', async () => {
    setViewport(390)
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})))
    try {
      render(<ActionBar item={film([file(5)])} onPlay={() => {}} deletion={{ itemId: 9, title: 'Дюна', onDeleted: () => {} }} />)
      expect(screen.queryByRole('button', { name: 'Удалить' })).toBeNull()
      await userEvent.click(screen.getByRole('button', { name: 'Ещё' }))
      const items = within(screen.getByRole('menu')).getAllByRole('menuitem')
      const last = items[items.length - 1]!
      expect(last).toHaveTextContent('Удалить…')
      await userEvent.click(last)
      expect(await screen.findByRole('alertdialog', { name: 'Удалить «Дюна»?' })).toBeInTheDocument()
    } finally {
      vi.unstubAllGlobals()
      setViewport(1440)
    }
  })
})
