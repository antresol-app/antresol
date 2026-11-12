// Round 5 item 2 in the editor: «Состав канала» (rules.picks — search,
// folders, the ordered list) and «Логотип» (upload, preview, remove).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Channel } from '../../../api/channels'
import { ChannelEditor } from '../ChannelEditor'

const json = (b: unknown, status = 200) => new Response(b === undefined ? null : JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

type Sent = { method: string; url: string; body: unknown }
let sent: Sent[] = []
let logoAnswer: () => Response = () => json({ logoUrl: '/api/channels/5/logo.png?v=3' })

beforeEach(() => {
  sent = []
  logoAnswer = () => json({ logoUrl: '/api/channels/5/logo.png?v=3' })
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: () => 'blob:logo', revokeObjectURL: () => {} }))
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      sent.push({ method, url, body: typeof init?.body === 'string' ? JSON.parse(init.body) : init?.body })
      if (url.endsWith('/api/channels/options'))
        return json({
          genres: [],
          decades: [],
          titles: [
            { id: 1, kind: 'series', title: 'Друзья', year: 1994, episodes: 236 },
            { id: 2, kind: 'movie', title: 'Матрица', year: 1999 },
          ],
          profiles: [],
        })
      if (url.startsWith('/api/channels/options/search'))
        return json({
          results: [
            { kind: 'episode', id: 77, title: 'Друзья', subtitle: 'S1 E7 · Отключение', year: 1994 },
            { kind: 'movie', id: 3, title: 'Много голосов', year: 2015 },
          ],
        })
      if (url.startsWith('/api/channels/options/folders')) {
        const path = new URL(url, 'http://x').searchParams.get('path') ?? ''
        if (!path) return json({ folders: [{ path: 'series', name: 'series' }], files: [] })
        if (path === 'series') return json({ folders: [{ path: 'series/Друзья/Season 01', name: 'Season 01' }], files: [{ id: 90, name: 'Пилот.mkv' }] })
        return json({ folders: [], files: [] })
      }
      if (url === '/api/items/55') return json({ id: 55, kind: 'episode', title: 'Друзья', subtitle: 'S2 E1' })
      if (url === '/api/channels/5' && method === 'PUT') return json({ ...CH, ...(JSON.parse(String(init!.body)) as object) })
      if (url === '/api/channels' && method === 'POST') return json({ ...CH, id: 9 }, 201)
      if (/\/api\/channels\/\d+\/logo$/.test(url) && method === 'POST') return logoAnswer()
      if (/\/api\/channels\/\d+\/logo$/.test(url) && method === 'DELETE') return new Response(null, { status: 204 })
      return json({ error: 'unexpected ' + url }, 500)
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

const CH: Channel = { id: 5, number: 7, name: 'Мой', category: '', personal: false, auto: false, enabled: true, canEdit: true, rules: { titles: [1, 2], order: 'ordered' } }

const lineup = () => screen.getByRole('list', { name: 'Состав канала' })
const names = () => within(lineup()).getAllByRole('listitem').map((li) => li.dataset.value)
const save = async () => {
  await userEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
  await waitFor(() => expect(sent.some((s) => s.method === 'PUT')).toBe(true))
  return sent.find((s) => s.method === 'PUT')!.body as { rules: { picks?: unknown[]; titles?: unknown } }
}

describe('«Состав канала»', () => {
  it('round 1’s titles show as picks and are saved as picks', async () => {
    const onSaved = vi.fn()
    render(<ChannelEditor channel={CH} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={onSaved} />)
    await screen.findByText('Матрица')
    expect(names()).toEqual(['title:1', 'title:2'])
    expect(screen.getByText('Сериал · 1994 · 236 серий')).toBeInTheDocument()
    // «по порядку» explained in one line
    expect(screen.getByText('Идут подряд, как в составе канала, и по кругу')).toBeInTheDocument()
    const body = await save()
    expect(body.rules.picks).toEqual([
      { kind: 'title', id: 1 },
      { kind: 'title', id: 2 },
    ])
    expect(body.rules.titles).toBeUndefined()
    await waitFor(() => expect(onSaved).toHaveBeenCalled())
  })

  it('adds from the search and the folders, reorders, removes and saves the list in order', async () => {
    render(<ChannelEditor channel={CH} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={() => {}} />)
    await screen.findByText('Матрица')
    await userEvent.type(screen.getByRole('searchbox', { name: 'Фильм, сериал или серия' }), 'др')
    await userEvent.click(await screen.findByRole('button', { name: 'Добавить: Много голосов' }))
    await userEvent.click(screen.getByRole('button', { name: 'Добавить: Друзья' }))
    expect(screen.getByRole('button', { name: 'Уже в составе: Много голосов' })).toBeDisabled()
    expect(names()).toEqual(['title:1', 'title:2', 'title:3', 'item:77'])

    // the folders: open «series», add «Season 01» and one file
    await userEvent.click(screen.getByRole('radio', { name: 'Папки' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Открыть: series' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Добавить: Season 01' }))
    await userEvent.click(screen.getByRole('button', { name: 'Добавить: Пилот.mkv' }))
    expect(names()).toEqual(['title:1', 'title:2', 'title:3', 'item:77', 'folder:series/Друзья/Season 01', 'item:90'])

    // reorder: the folder up two, the movie first by the keyboard; remove «Матрица»
    await userEvent.click(screen.getByRole('button', { name: 'Выше: Season 01' }))
    await userEvent.click(screen.getByRole('button', { name: 'Выше: Season 01' }))
    screen.getByRole('button', { name: 'Переместить: Много голосов' }).focus()
    await userEvent.keyboard('{Home}')
    await userEvent.click(screen.getByRole('button', { name: 'Убрать: Матрица' }))
    expect(names()).toEqual(['title:3', 'title:1', 'folder:series/Друзья/Season 01', 'item:77', 'item:90'])

    const body = await save()
    expect(body.rules.picks).toEqual([
      { kind: 'title', id: 3 },
      { kind: 'title', id: 1 },
      { kind: 'folder', path: 'series/Друзья/Season 01' },
      { kind: 'item', id: 77 },
      { kind: 'item', id: 90 },
    ])
  })

  it('drags a pick by its handle', async () => {
    render(<ChannelEditor channel={CH} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={() => {}} />)
    await screen.findByText('Матрица')
    within(lineup())
      .getAllByRole('listitem')
      .forEach((li, i) => {
        li.getBoundingClientRect = () => ({ top: i * 52, bottom: i * 52 + 52, height: 52, left: 0, right: 400, width: 400, x: 0, y: i * 52, toJSON: () => ({}) })
      })
    const h = screen.getByRole('button', { name: 'Переместить: Матрица' })
    fireEvent.pointerDown(h, { pointerId: 3, clientY: 70, button: 0 })
    fireEvent.pointerMove(h, { pointerId: 3, clientY: 10 })
    fireEvent.pointerUp(h, { pointerId: 3, clientY: 10 })
    expect(names()).toEqual(['title:2', 'title:1'])
  })

  it('an episode saved earlier is named from the library; an empty list cannot be saved', async () => {
    render(<ChannelEditor channel={{ ...CH, rules: { picks: [{ kind: 'item', id: 55 }], order: 'shuffle' } }} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={() => {}} />)
    expect(await screen.findByText('Серия · S2 E1')).toBeInTheDocument()
    expect(screen.getByText('В случайном порядке; сериал и папка — серия за серией')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Убрать: Друзья' }))
    expect(screen.getByText('Добавьте в состав хотя бы что-нибудь')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Сохранить' })).toBeDisabled()
  })
})

describe('«Состав канала» with the server’s pickInfo', () => {
  it('names the saved picks from pickInfo (no extra requests), marks the missing one', async () => {
    const ch: Channel = {
      ...CH,
      rules: { order: 'ordered', picks: [{ kind: 'item', id: 45 }, { kind: 'title', id: 17 }, { kind: 'folder', path: 'movies/Весёлый день (1994)' }] },
      pickInfo: [
        { kind: 'item', id: 45, title: 'Серия 8', subtitle: 'Смешные соседи · S1 E8', year: 2019 },
        { kind: 'title', id: 17, title: 'Мишка в городе', year: 2020, missing: true },
        { kind: 'folder', path: 'movies/Весёлый день (1994)', title: 'Весёлый день (1994)', subtitle: 'movies/Весёлый день (1994)' },
      ],
    }
    render(<ChannelEditor channel={ch} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={() => {}} />)
    expect(await screen.findByText('Серия · Смешные соседи · S1 E8 · 2019')).toBeInTheDocument()
    expect(screen.getByText('Сейчас не идёт · 2020')).toBeInTheDocument()
    expect(screen.getByText('Папка · movies/Весёлый день (1994)')).toBeInTheDocument()
    expect(sent.some((s) => s.url.startsWith('/api/items/'))).toBe(false)
  })
})

describe('«Логотип»', () => {
  const file = (type: string, size: number, name = 'logo.png') => {
    const f = new File(['x'], name, { type })
    Object.defineProperty(f, 'size', { value: size })
    return f
  }
  const choose = (f: File) => fireEvent.change(screen.getByTestId('logo-file'), { target: { files: [f] } })

  it('checks the file here: type and size', async () => {
    render(<ChannelEditor channel={CH} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={() => {}} />)
    expect(await screen.findByText('PNG, JPEG или WebP, до 2 МБ')).toBeInTheDocument()
    choose(file('image/gif', 1000, 'a.gif'))
    expect(await screen.findByRole('alert')).toHaveTextContent('Нужна картинка PNG, JPEG или WebP')
    choose(file('image/png', 3 * 1024 * 1024))
    expect(await screen.findByRole('alert')).toHaveTextContent('Файл больше 2 МБ')
    expect(sent.some((s) => s.url.endsWith('/logo'))).toBe(false)
  })

  it('uploads, previews, reports to the list and removes', async () => {
    const onLogoChanged = vi.fn()
    render(<ChannelEditor channel={CH} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={() => {}} onLogoChanged={onLogoChanged} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Загрузить…' }))
    choose(file('image/webp', 500_000, 'l.webp'))
    await waitFor(() => expect(document.querySelector('[data-channel-logo] img')?.getAttribute('src')).toBe('/api/channels/5/logo.png?v=3'))
    const up = sent.find((s) => s.method === 'POST' && s.url === '/api/channels/5/logo')!
    expect((up.body as FormData).get('file')).toBeInstanceOf(File)
    expect(screen.getByText('Логотип загружен')).toBeInTheDocument()
    expect(onLogoChanged).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('button', { name: 'Заменить…' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Убрать логотип' }))
    await waitFor(() => expect(document.querySelector('[data-channel-logo]')).toBeNull())
    expect(sent.some((s) => s.method === 'DELETE' && s.url === '/api/channels/5/logo')).toBe(true)
    expect(onLogoChanged).toHaveBeenCalledTimes(2)
  })

  it('shows the server’s refusal', async () => {
    logoAnswer = () => json({ error: 'Не картинка' }, 400)
    render(<ChannelEditor channel={CH} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={() => {}} />)
    await screen.findByRole('button', { name: 'Загрузить…' })
    choose(file('image/png', 1000))
    expect(await screen.findByRole('alert')).toHaveTextContent('Не картинка')
  })

  it('a new channel keeps the file and uploads it once created', async () => {
    const onSaved = vi.fn()
    render(<ChannelEditor channel={null} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={onSaved} />)
    await screen.findByRole('button', { name: 'Загрузить…' })
    choose(file('image/jpeg', 1000, 'l.jpg'))
    await waitFor(() => expect(document.querySelector('[data-channel-logo] img')?.getAttribute('src')).toBe('blob:logo'))
    expect(sent.some((s) => s.url.endsWith('/logo'))).toBe(false)
    await userEvent.type(screen.getByLabelText('Название'), 'Новый')
    await userEvent.click(screen.getByRole('button', { name: 'Создать' }))
    await waitFor(() => expect(onSaved).toHaveBeenCalled())
    expect(sent.some((s) => s.method === 'POST' && s.url === '/api/channels/9/logo')).toBe(true)
    expect(onSaved.mock.calls[0]![1]).toBeUndefined()
  })
})
