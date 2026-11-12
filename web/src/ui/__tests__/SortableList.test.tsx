// SortableList: any items, the caller's row content; a pointer (mouse, pen,
// finger) drags a row by its handle, the keyboard moves it with ↑/↓ on the
// handle or with «Выше» / «Ниже»; on a phone those go into the row's «⋯».
import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { SortableList, landing, moveItem } from '../SortableList'

type Pick = { key: string; title: string }
const P = (key: string, title: string): Pick => ({ key, title })
const START = [P('t:1', 'Друзья'), P('i:7', 'Серия 7'), P('f:a', 'Папка А'), P('t:2', 'Матрица')]

function Harness({ onChange, initial = START, moveButtons = true }: { onChange?: (v: Pick[]) => void; initial?: Pick[]; moveButtons?: boolean }) {
  const [items, setItems] = useState(initial)
  return (
    <SortableList
      label="Состав канала"
      items={items}
      getKey={(p) => p.key}
      itemLabel={(p) => p.title}
      renderItem={(p) => <span>{p.title}</span>}
      removable
      moveButtons={moveButtons}
      onChange={(v) => {
        setItems(v)
        onChange?.(v)
      }}
    />
  )
}

const order = () => within(screen.getByRole('list', { name: 'Состав канала' })).getAllByRole('listitem').map((li) => li.dataset.value)

function layOut() {
  const lis = within(screen.getByRole('list', { name: 'Состав канала' })).getAllByRole('listitem')
  lis.forEach((li, i) => {
    li.getBoundingClientRect = () => ({ top: i * 50, bottom: i * 50 + 50, height: 50, left: 0, right: 320, width: 320, x: 0, y: i * 50, toJSON: () => ({}) })
  })
  return lis
}

function phone(on: boolean) {
  vi.stubGlobal('matchMedia', (q: string) => ({ matches: on && /max-width/.test(q), media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
}

afterEach(() => vi.unstubAllGlobals())

describe('SortableList', () => {
  it('moveItem and landing are pure', () => {
    expect(moveItem([1, 2, 3], 0, 2)).toEqual([2, 3, 1])
    const same = [1, 2]
    expect(moveItem(same, 0, 5)).toBe(same)
    const d = { from: 0, tops: [0, 50, 100, 150], heights: [50, 50, 50, 50] }
    expect(landing(d, 0)).toBe(0)
    expect(landing(d, 60)).toBe(1)
    expect(landing(d, 160)).toBe(3)
  })

  it('drags a row with a mouse: the rows slide, the drop commits', () => {
    phone(false)
    const onChange = vi.fn()
    render(<Harness onChange={onChange} />)
    const [first, second] = layOut()
    const handle = screen.getByRole('button', { name: 'Переместить: Друзья' })
    fireEvent.pointerDown(handle, { pointerId: 1, pointerType: 'mouse', clientY: 25, button: 0 })
    fireEvent.pointerMove(handle, { pointerId: 1, pointerType: 'mouse', clientY: 130 })
    expect(first).toHaveAttribute('data-dragging')
    expect(first!.style.transform).toBe('translateY(105px)')
    expect(second!.style.transform).toBe('translateY(-50px)')
    expect(onChange).not.toHaveBeenCalled()
    fireEvent.pointerUp(handle, { pointerId: 1, pointerType: 'mouse', clientY: 130 })
    expect(onChange).toHaveBeenLastCalledWith([START[1], START[2], START[0], START[3]])
    expect(screen.getByRole('status')).toHaveTextContent('Друзья: 3 из 4')
  })

  it('drags with a finger the same way; Escape puts the row back', () => {
    phone(false)
    const onChange = vi.fn()
    render(<Harness onChange={onChange} />)
    layOut()
    const handle = screen.getByRole('button', { name: 'Переместить: Матрица' })
    fireEvent.pointerDown(handle, { pointerId: 7, pointerType: 'touch', clientY: 175, button: 0 })
    fireEvent.pointerMove(handle, { pointerId: 7, pointerType: 'touch', clientY: 20 })
    fireEvent.pointerUp(handle, { pointerId: 7, pointerType: 'touch', clientY: 20 })
    expect(order()).toEqual(['t:2', 't:1', 'i:7', 'f:a'])

    layOut()
    const h2 = screen.getByRole('button', { name: 'Переместить: Друзья' })
    fireEvent.pointerDown(h2, { pointerId: 8, pointerType: 'touch', clientY: 75, button: 0 })
    fireEvent.pointerMove(h2, { pointerId: 8, pointerType: 'touch', clientY: 190 })
    fireEvent.keyDown(window, { key: 'Escape' })
    fireEvent.pointerUp(h2, { pointerId: 8, pointerType: 'touch', clientY: 190 })
    expect(order()).toEqual(['t:2', 't:1', 'i:7', 'f:a'])
    expect(onChange).toHaveBeenCalledTimes(1)
  })

  it('the keyboard: ↑/↓/Home/End on the handle, the focus stays on it', async () => {
    phone(false)
    render(<Harness />)
    screen.getByRole('button', { name: 'Переместить: Папка А' }).focus()
    await userEvent.keyboard('{ArrowUp}')
    expect(order()).toEqual(['t:1', 'f:a', 'i:7', 't:2'])
    expect(screen.getByRole('button', { name: 'Переместить: Папка А' })).toHaveFocus()
    await userEvent.keyboard('{End}')
    expect(order()).toEqual(['t:1', 'i:7', 't:2', 'f:a'])
    await userEvent.keyboard('{Home}')
    expect(order()[0]).toBe('f:a')
    expect(screen.getByRole('button', { name: 'Переместить: Папка А' })).toHaveAccessibleDescription(/1 из 4/)
  })

  it('«Выше» / «Ниже» buttons move the row and keep the focus; at the edge the handle takes it', async () => {
    phone(false)
    render(<Harness />)
    expect(screen.getByRole('button', { name: 'Выше: Друзья' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Ниже: Матрица' })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: 'Ниже: Друзья' }))
    expect(order()).toEqual(['i:7', 't:1', 'f:a', 't:2'])
    expect(screen.getByRole('button', { name: 'Ниже: Друзья' })).toHaveFocus()
    await userEvent.click(screen.getByRole('button', { name: 'Выше: Друзья' }))
    expect(order()).toEqual(['t:1', 'i:7', 'f:a', 't:2'])
    // «Выше» is off at the top now: the row's handle has the focus
    expect(screen.getByRole('button', { name: 'Переместить: Друзья' })).toHaveFocus()
    await userEvent.click(screen.getByRole('button', { name: 'Убрать: Серия 7' }))
    expect(order()).toEqual(['t:1', 'f:a', 't:2'])
    expect(screen.getByRole('button', { name: 'Убрать: Папка А' })).toHaveFocus()
  })

  it('on a phone «Выше», «Ниже» and «Убрать» are in the row’s «⋯»', async () => {
    phone(true)
    render(<Harness />)
    expect(screen.queryByRole('button', { name: 'Выше: Серия 7' })).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Действия: Серия 7' }))
    const menu = await screen.findByRole('menu', { name: 'Серия 7' })
    await userEvent.click(within(menu).getByRole('menuitem', { name: 'Выше' }))
    expect(order()).toEqual(['i:7', 't:1', 'f:a', 't:2'])
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    await userEvent.click(screen.getByRole('button', { name: 'Действия: Матрица' }))
    await userEvent.click(within(await screen.findByRole('menu', { name: 'Матрица' })).getByRole('menuitem', { name: 'Убрать' }))
    expect(order()).toEqual(['i:7', 't:1', 'f:a'])
  })
})
