// An ordered list the Apple way: rows with a drag handle (the keyboard moves
// a row with ↑/↓ on it), a remove button, and «+ Добавить…» that opens a menu
// of what can still be added (and, for studios, a field for any name).
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { RankedList, type RankedListProps } from '../RankedList'

const LABELS: Record<string, string> = { ru: 'Русский', en: 'Английский', de: 'Немецкий', fr: 'Французский' }

function Harness({ initial, onChange, ...rest }: { initial: string[]; onChange?: (v: string[]) => void } & Partial<RankedListProps>) {
  const [items, setItems] = useState(initial)
  const set = (v: string[]) => {
    setItems(v)
    onChange?.(v)
  }
  return (
    <RankedList
      label="Мои языки"
      items={items}
      itemLabel={(v) => LABELS[v] ?? v}
      choices={() => Object.keys(LABELS).filter((c) => !items.includes(c)).map((c) => ({ value: c, label: LABELS[c]! }))}
      addLabel="Добавить язык"
      onChange={set}
      onAdd={(v) => set([...items, v])}
      {...rest}
    />
  )
}

const rows = () => within(screen.getByRole('list', { name: 'Мои языки' })).getAllByRole('listitem').map((li) => li.dataset.value)

describe('RankedList', () => {
  it('lists the items in order with their position for screen readers', () => {
    render(<Harness initial={['ru', 'en']} />)
    expect(rows()).toEqual(['ru', 'en'])
    expect(screen.getByRole('button', { name: 'Переместить: Русский' })).toHaveAccessibleDescription(/1 из 2/)
  })

  it('moves a row with the arrow keys on its handle, keeping the focus there', async () => {
    const onChange = vi.fn()
    render(<Harness initial={['ru', 'en', 'de']} onChange={onChange} />)
    const handle = screen.getByRole('button', { name: 'Переместить: Немецкий' })
    handle.focus()
    await userEvent.keyboard('{ArrowUp}')
    expect(onChange).toHaveBeenLastCalledWith(['ru', 'de', 'en'])
    expect(screen.getByRole('button', { name: 'Переместить: Немецкий' })).toHaveFocus()
    await userEvent.keyboard('{ArrowUp}{ArrowUp}')
    expect(rows()).toEqual(['de', 'ru', 'en'])
    expect(onChange).toHaveBeenCalledTimes(2) // at the top: nothing more
    await userEvent.keyboard('{End}')
    expect(rows()).toEqual(['ru', 'en', 'de'])
    expect(screen.getByRole('status')).toHaveTextContent('Немецкий: 3 из 3')
  })

  /** Lays the rows out 44px apart: jsdom has no layout. */
  function layOut() {
    const lis = within(screen.getByRole('list', { name: 'Мои языки' })).getAllByRole('listitem')
    lis.forEach((li, i) => {
      li.getBoundingClientRect = () => ({ top: i * 44, bottom: i * 44 + 44, height: 44, left: 0, right: 300, width: 300, x: 0, y: i * 44, toJSON: () => ({}) })
    })
    return lis
  }
  const shift = (li: HTMLElement) => li.style.transform

  it('drags a row by its handle: the rows slide, the DOM order stays until the drop', () => {
    const onChange = vi.fn()
    render(<Harness initial={['ru', 'en', 'de']} onChange={onChange} />)
    const [ru, en, de] = layOut()
    const handle = screen.getByRole('button', { name: 'Переместить: Русский' })
    fireEvent.pointerDown(handle, { pointerId: 1, clientY: 20, button: 0 })
    fireEvent.pointerMove(handle, { pointerId: 1, clientY: 120 })
    // the dragged row follows the pointer, the others make room; no node moves
    // (moving the node under the pointer would drop its pointer capture)
    expect(rows()).toEqual(['ru', 'en', 'de'])
    expect(shift(ru!)).toBe('translateY(100px)')
    expect(shift(en!)).toBe('translateY(-44px)')
    expect(shift(de!)).toBe('translateY(-44px)')
    expect(onChange).not.toHaveBeenCalled()
    fireEvent.pointerUp(handle, { pointerId: 1, clientY: 120 })
    expect(onChange).toHaveBeenLastCalledWith(['en', 'de', 'ru'])
    expect(rows()).toEqual(['en', 'de', 'ru'])
    for (const li of within(screen.getByRole('list', { name: 'Мои языки' })).getAllByRole('listitem')) expect(shift(li)).toBe('')
  })

  it('a lost pointer capture or Escape cancels the drag', () => {
    const onChange = vi.fn()
    render(<Harness initial={['ru', 'en', 'de']} onChange={onChange} />)
    let [ru] = layOut()
    let handle = screen.getByRole('button', { name: 'Переместить: Русский' })
    fireEvent.pointerDown(handle, { pointerId: 1, clientY: 20, button: 0 })
    fireEvent.pointerMove(handle, { pointerId: 1, clientY: 120 })
    fireEvent(handle, new Event('lostpointercapture', { bubbles: true }))
    expect(shift(ru!)).toBe('')
    expect(ru).not.toHaveAttribute('data-dragging')
    fireEvent.pointerUp(handle, { pointerId: 1, clientY: 120 })
    expect(onChange).not.toHaveBeenCalled()
    ;[ru] = layOut()
    handle = screen.getByRole('button', { name: 'Переместить: Русский' })
    fireEvent.pointerDown(handle, { pointerId: 2, clientY: 20, button: 0 })
    fireEvent.pointerMove(handle, { pointerId: 2, clientY: 120 })
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(shift(ru!)).toBe('')
    fireEvent.pointerUp(handle, { pointerId: 2, clientY: 120 })
    expect(onChange).not.toHaveBeenCalled()
    expect(rows()).toEqual(['ru', 'en', 'de'])
  })

  it('removes a row; the focus goes to the next row’s remove button; `min` rows stay', async () => {
    const onChange = vi.fn()
    render(<Harness initial={['ru', 'en', 'de']} min={1} onChange={onChange} />)
    await userEvent.click(screen.getByRole('button', { name: 'Убрать: Английский' }))
    expect(onChange).toHaveBeenLastCalledWith(['ru', 'de'])
    expect(screen.getByRole('button', { name: 'Убрать: Немецкий' })).toHaveFocus()
    await userEvent.click(screen.getByRole('button', { name: 'Убрать: Немецкий' }))
    expect(screen.getByRole('button', { name: 'Убрать: Русский' })).toBeDisabled()
    // the row that is left cannot be removed: its handle takes the focus, not the page
    expect(screen.getByRole('button', { name: 'Переместить: Русский' })).toHaveFocus()
  })

  it('the last row removed: the focus goes to «Добавить»', async () => {
    render(<Harness initial={['ru']} />)
    await userEvent.click(screen.getByRole('button', { name: 'Убрать: Русский' }))
    expect(screen.getByRole('button', { name: 'Добавить язык' })).toHaveFocus()
  })

  it('adds from the menu of what is not in the list yet', async () => {
    const onChange = vi.fn()
    render(<Harness initial={['ru']} onChange={onChange} />)
    await userEvent.click(screen.getByRole('button', { name: 'Добавить язык' }))
    const menu = screen.getByRole('menu', { name: 'Добавить язык' })
    expect(within(menu).getAllByRole('menuitem').map((i) => i.textContent)).toEqual(['Английский', 'Немецкий', 'Французский'])
    await userEvent.click(within(menu).getByRole('menuitem', { name: 'Французский' }))
    expect(onChange).toHaveBeenLastCalledWith(['ru', 'fr'])
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
  })

  it('no «Добавить» when nothing is left or the list is full', () => {
    const { unmount } = render(<Harness initial={['ru', 'en', 'de', 'fr']} />)
    expect(screen.queryByRole('button', { name: 'Добавить язык' })).toBeNull()
    unmount()
    render(<Harness initial={['ru', 'en']} max={2} />)
    expect(screen.queryByRole('button', { name: 'Добавить язык' })).toBeNull()
  })

  it('an empty list says so', () => {
    render(<Harness initial={[]} empty="Пока ничего" />)
    expect(screen.getByText('Пока ничего')).toBeInTheDocument()
  })
})
