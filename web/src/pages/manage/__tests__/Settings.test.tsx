import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Settings as Values } from '../../../api/types'
import { Settings } from '../Settings'

const base: Values = {
  quality: 1080, voice: 'dub', maxSizeGB: 0, uploadLimited: false, uploadLimitKBs: 0, streamCacheGB: 50, remoteQuality: '1080',
  remoteMusic: 'high', preferLossless: true, autoSubtitles: true, subtitleLangs: ['ru', 'en'], subtitlesAvailable: false,
}
const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

function stub(v: Values) {
  const puts: Values[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/api/settings') && init?.method === 'PUT') {
        const body = JSON.parse(String(init.body)) as Values
        puts.push(body)
        return json({ ...body, subtitlesAvailable: v.subtitlesAvailable })
      }
      if (url.endsWith('/api/settings')) return json(v)
      if (url.endsWith('/api/library/status')) return json({ running: false, queued: false, movies: 0, episodes: 0 })
      return json({})
    }),
  )
  return puts
}

describe('Настройки: музыка и субтитры', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('«Музыка вне дома» is saved; without a key the subtitles switch is off and says why', async () => {
    const puts = stub(base)
    render(<Settings />)
    const music = await screen.findByRole('radiogroup', { name: 'Музыка вне дома' })
    expect(screen.getByText('Высокое — AAC 256 кбит/с, экономно — 128 кбит/с')).toBeInTheDocument()
    expect(within(music).getByRole('radio', { name: 'Высокое' })).toHaveAttribute('aria-checked', 'true')
    await userEvent.click(within(music).getByRole('radio', { name: 'Экономно' }))
    await waitFor(() => expect(puts.at(-1)?.remoteMusic).toBe('low'))

    const auto = screen.getByRole('switch', { name: 'Искать субтитры автоматически' })
    expect(auto).toBeDisabled()
    expect(screen.getByText('Нужен ключ OpenSubtitles в настройках сервера')).toBeInTheDocument()
    // the languages stay visible, in Russian: a pop-up that lists them
    expect(screen.getByRole('button', { name: 'Языки субтитров: Русский, Английский' })).toBeInTheDocument()
  })

  it('«Мои языки»: added, reordered and removed; subtitles keep only them, in their order', async () => {
    const puts = stub({ ...base, subtitlesAvailable: true, languages: ['ru', 'en'] })
    render(<Settings />)
    const mine = await screen.findByRole('list', { name: 'Мои языки' })
    expect(within(mine).getAllByRole('listitem').map((li) => li.dataset.value)).toEqual(['ru', 'en'])
    await userEvent.click(screen.getByRole('button', { name: 'Добавить язык' }))
    await userEvent.click(screen.getByRole('menuitem', { name: 'Японский' }))
    await waitFor(() => expect(puts.at(-1)?.languages).toEqual(['ru', 'en', 'ja']))
    // only the chosen languages are offered for subtitles
    await userEvent.click(screen.getByRole('button', { name: 'Языки субтитров: Русский, Английский' }))
    expect(screen.getAllByRole('menuitemcheckbox').map((i) => i.textContent)).toEqual(['Русский', 'Английский', 'Японский'])
    await userEvent.keyboard('{Escape}')
    // reordered by the handle: English becomes the main language
    screen.getByRole('button', { name: 'Переместить: Английский' }).focus()
    await userEvent.keyboard('{ArrowUp}')
    await waitFor(() => expect(puts.at(-1)?.languages).toEqual(['en', 'ru', 'ja']))
    // without Russian: subtitles lose it
    await userEvent.click(screen.getByRole('button', { name: 'Убрать: Русский' }))
    await waitFor(() => expect(puts.at(-1)).toMatchObject({ languages: ['en', 'ja'], subtitleLangs: ['en'] }))
    expect(within(mine).getAllByRole('listitem').map((li) => li.dataset.value)).toEqual(['en', 'ja'])
  })

  it('«Превью при перемотке»: on when the server says nothing, saved when turned off', async () => {
    const puts = stub(base)
    render(<Settings />)
    const sw = await screen.findByRole('switch', { name: 'Превью при перемотке' })
    expect(sw).toBeChecked()
    await userEvent.click(sw)
    await waitFor(() => expect(puts.at(-1)?.trickplay).toBe(false))
    // off: no space to choose
    expect(screen.queryByRole('radiogroup', { name: 'Место для превью' })).not.toBeInTheDocument()
  })

  it('«Место для превью»: 10 ГБ by default, saved when changed', async () => {
    const puts = stub(base)
    render(<Settings />)
    const space = await screen.findByRole('radiogroup', { name: 'Место для превью' })
    expect(within(space).getByRole('radio', { name: '10 ГБ' })).toHaveAttribute('aria-checked', 'true')
    await userEvent.click(within(space).getByRole('radio', { name: '20 ГБ' }))
    await waitFor(() => expect(puts.at(-1)?.trickplayBudgetGB).toBe(20))
  })

  it('subtitle languages: at least one stays on', async () => {
    const puts = stub({ ...base, subtitlesAvailable: true })
    render(<Settings />)
    const auto = await screen.findByRole('switch', { name: 'Искать субтитры автоматически' })
    expect(auto).toBeEnabled()
    await userEvent.click(screen.getByRole('button', { name: 'Языки субтитров: Русский, Английский' }))
    await userEvent.click(screen.getByRole('menuitemcheckbox', { name: 'Английский' }))
    await waitFor(() => expect(puts.at(-1)?.subtitleLangs).toEqual(['ru']))
    expect(screen.getByRole('menuitemcheckbox', { name: 'Русский' })).toHaveAttribute('aria-disabled', 'true')
  })
})

describe('Настройки: языки интерфейса и описаний', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('«Язык интерфейса» → English re-renders the page in English and remembers it in this browser', async () => {
    stub(base)
    render(<Settings />)
    const ui = await screen.findByRole('radiogroup', { name: 'Язык интерфейса' })
    expect(within(ui).getByRole('radio', { name: 'Как в системе' })).toHaveAttribute('aria-checked', 'true')
    expect(document.documentElement.lang).toBe('ru')
    await userEvent.click(within(ui).getByRole('radio', { name: 'English' }))
    expect(await screen.findByRole('heading', { name: 'Settings', level: 2 })).toBeInTheDocument()
    expect(screen.getByRole('radiogroup', { name: 'Music away from home' })).toBeInTheDocument()
    expect(within(screen.getByRole('radiogroup', { name: 'Interface language' })).getByRole('radio', { name: 'English' })).toHaveAttribute('aria-checked', 'true')
    expect(document.documentElement.lang).toBe('en')
    expect(localStorage.getItem('vault.uiLanguage')).toBe('en')
    // back to the system language (Russian in the tests)
    await userEvent.click(within(screen.getByRole('radiogroup', { name: 'Interface language' })).getByRole('radio', { name: 'Same as system' }))
    expect(await screen.findByRole('heading', { name: 'Настройки', level: 2 })).toBeInTheDocument()
    expect(document.documentElement.lang).toBe('ru')
  })

  it('«Язык описаний медиатеки» shows only when the server has it and saves metadataLanguage', async () => {
    stub(base)
    const { unmount } = render(<Settings />)
    await screen.findByRole('radiogroup', { name: 'Язык интерфейса' })
    expect(screen.queryByRole('radiogroup', { name: 'Язык описаний медиатеки' })).not.toBeInTheDocument()
    unmount()
    vi.unstubAllGlobals()

    const puts = stub({ ...base, metadataLanguage: 'ru' })
    render(<Settings />)
    const meta = await screen.findByRole('radiogroup', { name: 'Язык описаний медиатеки' })
    expect(screen.getByText('Для новых сканирований: названия и описания из TMDB')).toBeInTheDocument()
    expect(within(meta).getByRole('radio', { name: 'Русский' })).toHaveAttribute('aria-checked', 'true')
    await userEvent.click(within(meta).getByRole('radio', { name: 'English' }))
    await waitFor(() => expect(puts.at(-1)?.metadataLanguage).toBe('en'))
  })
})

describe('Настройки: любимые озвучки', () => {
  afterEach(async () => {
    vi.unstubAllGlobals()
    const { setLanguagePref } = await import('../../../i18n')
    setLanguagePref('ru')
  })

  it.runIf(__LIBRARY_EDITION__)('core build: «Любимые озвучки» в «Просмотр», без слов о раздачах', async () => {
    stub({ ...base, languages: ['ru', 'en'] })
    render(<Settings />)
    expect(await screen.findByText('Любимые озвучки')).toBeInTheDocument()
    expect(screen.getByText('Очищенные озвучки делаются в этом порядке: первая — главная.')).toBeInTheDocument()
    expect(screen.getByText('Пока ни одной.')).toBeInTheDocument()
    expect(document.body.textContent).not.toMatch(/[Рр]аздач/)
  })
})

// RescanLink: the empty pages of the core build land on the last row of the page.
describe('Настройки: /manage/settings#rescan', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    window.location.hash = ''
    delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView
  })

  it('scrolls to the rescan row, and not without the hash', async () => {
    const scroll = vi.fn()
    Element.prototype.scrollIntoView = scroll
    stub(base)
    const first = render(<Settings />)
    await screen.findByText('Пересканировать медиатеку')
    expect(scroll).not.toHaveBeenCalled()
    first.unmount()

    window.location.hash = '#rescan'
    render(<Settings />)
    await screen.findByText('Пересканировать медиатеку')
    // a passive effect: it may run just after the row is in the document
    await waitFor(() => expect(scroll).toHaveBeenCalledOnce())
    expect(scroll.mock.contexts[0]).toHaveTextContent('Пересканировать медиатеку')
  })
})

// «Отправка на Kindle»: one row like the others — its state and «Настроить…»;
// the steps and the SMTP fields live in a sheet.
describe('Настройки: отправка на Kindle', () => {
  afterEach(() => vi.unstubAllGlobals())
  const mail = { host: 'smtp.gmail.com', port: 587, username: 'me@example.com', from: 'me@example.com' }

  it('a set-up mailbox: one row with its address and «Настроить…», no fields on the page', async () => {
    stub({ ...base, mail, mailPasswordSet: true })
    render(<Settings />)
    expect(await screen.findByText('Настроена · me@example.com')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Настроить…' })).toBeInTheDocument()
    expect(screen.queryByRole('textbox', { name: 'Сервер SMTP' })).toBeNull()
    expect(screen.queryByText(/Создайте пароль для отправки/)).toBeNull()
  })

  it('not set up: says so', async () => {
    stub(base)
    render(<Settings />)
    expect(await screen.findByText('Не настроена')).toBeInTheDocument()
  })

  it('«Настроить…» opens the steps and the fields; a field saves when left; «Готово» closes', async () => {
    const puts = stub({ ...base, mail, mailPasswordSet: true })
    render(<Settings />)
    await userEvent.click(await screen.findByRole('button', { name: 'Настроить…' }))
    const sheet = screen.getByRole('dialog', { name: 'Отправка на Kindle' })
    expect(within(sheet).getByText(/Создайте пароль для отправки/)).toBeInTheDocument()
    const host = within(sheet).getByRole('textbox', { name: 'Сервер SMTP' })
    await userEvent.clear(host)
    await userEvent.type(host, 'smtp.example.org')
    await userEvent.click(within(sheet).getByRole('textbox', { name: 'Логин' }))
    await waitFor(() => expect(puts.at(-1)?.mail?.host).toBe('smtp.example.org'))
    await userEvent.click(within(sheet).getByRole('button', { name: 'Готово' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(await screen.findByText('Настроена · me@example.com')).toBeInTheDocument()
  })

  it('Enter in a field saves it and closes the sheet', async () => {
    const puts = stub({ ...base, mail, mailPasswordSet: true })
    render(<Settings />)
    await userEvent.click(await screen.findByRole('button', { name: 'Настроить…' }))
    const from = within(screen.getByRole('dialog', { name: 'Отправка на Kindle' })).getByRole('textbox', { name: 'Адрес отправителя' })
    await userEvent.clear(from)
    await userEvent.type(from, 'books@example.com{Enter}')
    await waitFor(() => expect(puts.at(-1)?.mail?.from).toBe('books@example.com'))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })
})
