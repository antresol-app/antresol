import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AccountsState, GoodreadsState, ShikimoriState, User } from '../../../api/types'
import { resetAccounts } from '../../../state/accounts'
import { session } from '../../../state/session'
import { GoodreadsAccount } from '../GoodreadsAccount'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const adult: User = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const kid: User = { ...adult, id: 3, name: 'Вася', isKid: true, maxRating: '12+' }

const shiki: ShikimoriState = { linked: false, nickname: '', status: 'ok', lastError: '', lastSync: null, syncing: false, writeState: 'unasked', canWrite: true, unmapped: 0 }
const none: GoodreadsState = { linked: false, nickname: '', status: 'ok', lastError: '', lastSync: null, syncing: false, total: 0, read: 0, reading: 0, toRead: 0, inLibrary: 0 }
const books = { total: 12, read: 8, reading: 1, toRead: 3, inLibrary: 5 }
const linked = (p: Partial<GoodreadsState> = {}): GoodreadsState => ({
  ...none,
  linked: true,
  nickname: 'some-name',
  lastSync: new Date(Date.now() - 5 * 60000).toISOString(),
  ...books,
  ...p,
})

let state: GoodreadsState
let offered: boolean
let calls: string[]
let importStatus = 200
let syncRefusal = ''
function serve(user: User) {
  calls = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      calls.push(`${method} ${url}${init?.body ? ' ' + String(init.body) : ''}`)
      if (url.endsWith('/api/login')) return json(user)
      if (url === '/api/accounts' && method === 'GET') return json((offered ? { shikimori: shiki, goodreads: state } : { shikimori: shiki }) satisfies AccountsState)
      if (url === '/api/accounts/goodreads' && method === 'PUT') {
        state = linked({ lastSync: null, syncing: true, total: 0, read: 0, reading: 0, toRead: 0, inLibrary: 0 })
        return json({ shikimori: shiki, goodreads: state })
      }
      if (url === '/api/accounts/goodreads/import' && method === 'POST') {
        if (importStatus === 413) return new Response('<html>413 Request Entity Too Large</html>', { status: 413 })
        if (importStatus !== 200) return json({ error: 'Это не экспорт библиотеки Goodreads' }, importStatus)
        state = { ...state, ...books }
        return json({ shikimori: shiki, goodreads: state })
      }
      if (url === '/api/accounts/goodreads' && method === 'DELETE') return new Response(null, { status: 204 })
      if (url === '/api/accounts/goodreads/sync' && method === 'POST') {
        if (syncRefusal) return json({ error: syncRefusal }, 429)
        state = { ...state, syncing: true }
        return json({ shikimori: shiki, goodreads: state }, 202)
      }
      return json({})
    }),
  )
}
const open = async (user = adult) => {
  serve(user)
  await session.login(user.id)
  return render(<GoodreadsAccount />)
}
const csv = (text = 'Title,Author,Exclusive Shelf\nDune,Frank Herbert,read\n', name = 'goodreads_library_export.csv') => new File([text], name, { type: 'text/csv' })

beforeEach(() => {
  session.reset()
  resetAccounts()
  state = none
  offered = true
  importStatus = 200
  syncRefusal = ''
})
afterEach(() => vi.unstubAllGlobals())

describe('GoodreadsAccount', () => {
  it('links by profile address and says what Goodreads allows', async () => {
    await open()
    expect(await screen.findByText(/не больше 100 последних книг с полки/)).toBeInTheDocument()
    await userEvent.type(await screen.findByLabelText('Адрес профиля на Goodreads'), 'https://www.goodreads.com/user/show/12345-some-name')
    await userEvent.click(screen.getByRole('button', { name: 'Связать' }))
    expect(await screen.findByText('Связан как some-name')).toBeInTheDocument()
    expect(calls).toContain('PUT /api/accounts/goodreads {"profile":"https://www.goodreads.com/user/show/12345-some-name"}')
    expect(screen.getByText('Читаем полки с Goodreads, это займёт несколько секунд')).toBeInTheDocument()
  })

  it('syncs now: says it runs, then shows how it went and the new books', async () => {
    state = linked({ total: 3, read: 1, reading: 1, toRead: 1, inLibrary: 1 })
    await open()
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      await userEvent.click(await screen.findByRole('button', { name: 'Синхронизировать' }))
      expect(calls).toContain('POST /api/accounts/goodreads/sync')
      expect(await screen.findByText('Синхронизация…')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Синхронизировать' })).toBeDisabled()
      // not the first read: no «займёт несколько секунд» line
      expect(screen.queryByText('Читаем полки с Goodreads, это займёт несколько секунд')).toBeNull()
      state = linked({ lastSync: new Date().toISOString(), syncing: false }) // it ended on the server
      await vi.advanceTimersByTimeAsync(2100)
      expect(await screen.findByText('Синхронизировано только что')).toBeInTheDocument()
      expect(screen.getByText(/Книг с Goodreads: 12 /)).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Синхронизировать' })).toBeEnabled()
      const gets = () => calls.filter((c) => c === 'GET /api/accounts').length
      const n = gets()
      await vi.advanceTimersByTimeAsync(20000)
      expect(gets()).toBe(n) // stopped once it ended
    } finally {
      vi.useRealTimers()
    }
  })

  it('says why a sync was not started', async () => {
    state = linked()
    syncRefusal = 'Только что синхронизировано, повторите через минуту'
    await open()
    await userEvent.click(await screen.findByRole('button', { name: 'Синхронизировать' }))
    expect(await screen.findByText('Только что синхронизировано, повторите через минуту')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Синхронизировать' })).toBeEnabled()
    expect(screen.queryByText('Синхронизация…')).toBeNull()
  })

  it('shows the books, the read-only line and the status line of a linked profile', async () => {
    state = linked()
    await open()
    expect(await screen.findByText(/Только чтение: полки с Goodreads попадают в «Книги»/)).toBeInTheDocument()
    expect(screen.getByText(/Синхронизировано .*/)).toBeInTheDocument()
    expect(screen.getByText(/Книг с Goodreads: 12 \(прочитано 8, читаю 1, хочу прочитать 3\), в библиотеке нашлось 5/)).toBeInTheDocument()
    // a linked profile has no «Забыть список»: «Отвязать» does it
    expect(screen.queryByRole('button', { name: 'Забыть список' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Отвязать' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Синхронизировать' })).toBeEnabled()
  })

  it('says why a profile gives nothing', async () => {
    state = linked({ total: 0, read: 0, reading: 0, toRead: 0, inLibrary: 0 })
    const { unmount } = await open()
    expect(await screen.findByText(/На полках профиля пока нет книг/)).toBeInTheDocument()
    unmount()
    resetAccounts()
    state = linked({ status: 'not_found' })
    await open()
    expect(await screen.findByText('Профиль на Goodreads не найден или скрыт.')).toBeInTheDocument()
  })

  it('imports the export file as text, with no link', async () => {
    await open()
    const input = await screen.findByTestId('goodreads-file')
    fireEvent.change(input, { target: { files: [csv()] } })
    expect(await screen.findByText('Файл прочитан: 12 книг, в библиотеке нашлось 5.')).toBeInTheDocument()
    expect(calls).toContain('POST /api/accounts/goodreads/import {"csv":"Title,Author,Exclusive Shelf\\nDune,Frank Herbert,read\\n"}')
    // the books stay listed, with a way to forget them (nothing is linked)
    expect(screen.getByText(/Книг с Goodreads: 12/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Забыть список' }))
    await waitFor(() => expect(calls).toContain('DELETE /api/accounts/goodreads'))
    await waitFor(() => expect(screen.queryByText(/Книг с Goodreads/)).toBeNull())
  })

  it('shows the server\'s refusal of a file that is not an export', async () => {
    importStatus = 422
    await open()
    fireEvent.change(await screen.findByTestId('goodreads-file'), { target: { files: [csv('a,b\n1,2\n', 'other.csv')] } })
    expect(await screen.findByText('Это не экспорт библиотеки Goodreads')).toBeInTheDocument()
  })

  it('does not send a file over the limit', async () => {
    await open()
    const big = csv()
    Object.defineProperty(big, 'size', { value: 25 * 1024 * 1024 })
    fireEvent.change(await screen.findByTestId('goodreads-file'), { target: { files: [big] } })
    expect(await screen.findByText('Файл больше 24 МБ.')).toBeInTheDocument()
    expect(calls.some((c) => c.includes('/goodreads/import'))).toBe(false)
  })

  it('names a refusal of the file by size, a proxy\'s too', async () => {
    importStatus = 413
    await open()
    fireEvent.change(await screen.findByTestId('goodreads-file'), { target: { files: [csv()] } })
    // the proxy's own answer, with no message of ours
    expect(await screen.findByText('Файл больше 24 МБ.')).toBeInTheDocument()
  })

  it('unlinks', async () => {
    state = linked()
    await open()
    await userEvent.click(await screen.findByRole('button', { name: 'Отвязать' }))
    expect(await screen.findByLabelText('Адрес профиля на Goodreads')).toBeInTheDocument()
    expect(calls).toContain('DELETE /api/accounts/goodreads')
  })

  it('is absent for a kid and on a server without it', async () => {
    const a = await open(kid)
    expect(a.container).toBeEmptyDOMElement()
    a.unmount()
    session.reset()
    resetAccounts()
    offered = false
    const b = await open()
    await waitFor(() => expect(calls).toContain('GET /api/accounts'))
    expect(b.container).toBeEmptyDOMElement()
  })
})
