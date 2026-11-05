import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { DeleteItem } from '../DeleteItem'
import { Notice } from '../Notice'
import { clearNotice } from '../../state/notice'
import type { DeletePlan } from '../../api/types'

const GB = 1024 ** 3
let plan: DeletePlan
let calls: string[] = []
let failDelete = ''

const series: DeletePlan = {
  id: 10,
  kind: 'series',
  title: 'Сёгун',
  size: 6 * GB,
  files: 3,
  seasons: [
    { id: 11, number: 1, title: 'Сезон 1', episodes: 2, size: 4 * GB, notes: ['Раздача «S01» прекратится'] },
    { id: 12, number: 2, title: 'Сезон 2', episodes: 1, size: 2 * GB, notes: [] },
  ],
  notes: [],
}

beforeEach(() => {
  plan = series
  calls = []
  failDelete = ''
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push(`${method} ${String(input)}`)
      if (method === 'POST') {
        if (failDelete) return new Response(JSON.stringify({ error: failDelete }), { status: 409 })
        const id = Number(String(input).split('/')[3])
        return new Response(JSON.stringify({ items: [id], size: 1, trashed: 1, parentId: id === 10 ? 0 : 10, parentShown: id !== 10 }), { status: 200 })
      }
      return new Response(JSON.stringify(plan), { status: 200 })
    }),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
  act(() => clearNotice())
})

describe('DeleteItem', () => {
  it('asks which season of a series to delete and deletes the one picked', async () => {
    const onDeleted = vi.fn()
    render(
      <>
        <DeleteItem itemId={10} title="Сёгун" onDeleted={onDeleted} />
        <Notice />
      </>,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Удалить' }))
    const sheet = await screen.findByRole('alertdialog', { name: 'Удалить «Сёгун»?' })
    await within(sheet).findByText('Файлы переместятся в Корзину в «Файлах» — оттуда их можно восстановить.')
    expect(within(sheet).getByText('Освободится 6,0 ГБ, когда Корзину очистят.')).toBeInTheDocument()
    expect(within(sheet).getByRole('radio', { name: /Весь сериал/ })).toBeChecked()
    // the notes are the chosen season's
    expect(within(sheet).queryByText('Раздача «S01» прекратится')).toBeNull()
    fireEvent.click(within(sheet).getByRole('radio', { name: /Сезон 2/ }))
    expect(within(sheet).queryByText('Раздача «S01» прекратится')).toBeNull()
    fireEvent.click(within(sheet).getByRole('radio', { name: /Сезон 1/ }))
    expect(within(sheet).getByText('Раздача «S01» прекратится')).toBeInTheDocument()
    expect(within(sheet).getByText('Освободится 4,0 ГБ, когда Корзину очистят.')).toBeInTheDocument()
    expect(within(sheet).getByText('2 серии · 4,0 ГБ')).toBeInTheDocument()
    fireEvent.click(within(sheet).getByRole('button', { name: 'Удалить' }))
    await waitFor(() => expect(onDeleted).toHaveBeenCalled())
    expect(calls).toContain('POST /api/items/11/delete')
    expect(onDeleted.mock.calls[0]![0]).toMatchObject({ id: 11, whole: false })
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('«Сезон 1» в Корзине')
  })

  it('deletes a movie without a choice and shows the server’s refusal in plain words', async () => {
    plan = { id: 5, kind: 'movie', title: 'Брат', size: 2 * GB, files: 1, seasons: [], notes: [] }
    failDelete = 'Медиадиск недоступен, ничего не удалено. Подключите его и попробуйте ещё раз'
    const onDeleted = vi.fn()
    render(<DeleteItem itemId={5} title="Брат" onDeleted={onDeleted} />)
    fireEvent.click(screen.getByRole('button', { name: 'Удалить' }))
    const sheet = await screen.findByRole('alertdialog', { name: 'Удалить «Брат»?' })
    await within(sheet).findByText('Освободится 2,0 ГБ, когда Корзину очистят.')
    expect(within(sheet).queryByRole('radio')).not.toBeInTheDocument()
    fireEvent.click(within(sheet).getByRole('button', { name: 'Удалить' }))
    expect(await within(sheet).findByRole('alert')).toHaveTextContent('Медиадиск недоступен')
    expect(onDeleted).not.toHaveBeenCalled()
    expect(calls).toContain('POST /api/items/5/delete')
  })

  it('the server’s note about late files is in the confirmation', async () => {
    plan = { id: 5, kind: 'movie', title: 'Брат', size: GB, files: 1, seasons: [], notes: [] }
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) =>
        init?.method === 'POST'
          ? new Response(JSON.stringify({ items: [5], size: 1, trashed: 1, parentId: 0, parentShown: false, note: 'Новых файлов тоже в Корзине: 1' }), { status: 200 })
          : new Response(JSON.stringify(plan), { status: 200 }),
      ),
    )
    render(
      <>
        <DeleteItem itemId={5} title="Брат" onDeleted={() => {}} />
        <Notice />
      </>,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Удалить' }))
    const sheet = await screen.findByRole('alertdialog', { name: 'Удалить «Брат»?' })
    await within(sheet).findByText('Освободится 1,0 ГБ, когда Корзину очистят.')
    fireEvent.click(within(sheet).getByRole('button', { name: 'Удалить' }))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('«Брат» в Корзине. Новых файлов тоже в Корзине: 1'))
  })

  it('a whole series is gone after its delete', async () => {
    const onDeleted = vi.fn()
    render(<DeleteItem itemId={10} title="Сёгун" onDeleted={onDeleted} />)
    fireEvent.click(screen.getByRole('button', { name: 'Удалить' }))
    const sheet = await screen.findByRole('alertdialog', { name: 'Удалить «Сёгун»?' })
    await within(sheet).findByRole('radio', { name: /Весь сериал/ })
    fireEvent.click(within(sheet).getByRole('button', { name: 'Удалить' }))
    await waitFor(() => expect(onDeleted).toHaveBeenCalled())
    expect(onDeleted.mock.calls[0]![0]).toMatchObject({ id: 10, whole: true })
  })
})
