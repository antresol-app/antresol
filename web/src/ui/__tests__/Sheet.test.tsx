import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { Sheet } from '../Sheet'

describe('Sheet', () => {
  it('makes the page behind it inert while open and gives it back', () => {
    const page = document.createElement('div')
    page.id = 'page'
    const alreadyInert = document.createElement('div')
    alreadyInert.setAttribute('inert', '')
    document.body.append(page, alreadyInert)
    const { unmount } = render(<Sheet title="Удалить?" onCancel={() => {}} actions={[{ label: 'Отменить', kind: 'cancel', run: () => {} }]} />)
    const sheet = document.querySelector('[role="alertdialog"]')!
    expect(page.hasAttribute('inert')).toBe(true)
    expect(sheet.parentElement!.hasAttribute('inert')).toBe(false)
    unmount()
    expect(page.hasAttribute('inert')).toBe(false)
    expect(alreadyInert.hasAttribute('inert')).toBe(true) // not ours to give back
    page.remove()
    alreadyInert.remove()
  })

  it('closes a read-only picker on a click outside it, not on a press that started inside', () => {
    const onCancel = vi.fn()
    const { unmount } = render(<Sheet title="Раздачи" role="dialog" dismissOnOutside onCancel={onCancel} />)
    const sheet = document.querySelector('[role="dialog"]')!
    const backdrop = sheet.parentElement!
    fireEvent.mouseDown(sheet)
    fireEvent.click(backdrop)
    expect(onCancel).not.toHaveBeenCalled()
    fireEvent.mouseDown(backdrop)
    fireEvent.click(backdrop)
    expect(onCancel).toHaveBeenCalledTimes(1)
    unmount()
  })

  it('keeps a form open on a click outside it (what was typed is not thrown away)', () => {
    const onCancel = vi.fn()
    const { unmount } = render(<Sheet title="Новый профиль" role="dialog" onCancel={onCancel} actions={[{ label: 'Отменить', kind: 'cancel', run: () => {} }]}><input /></Sheet>)
    const backdrop = document.querySelector('[role="dialog"]')!.parentElement!
    fireEvent.mouseDown(backdrop)
    fireEvent.click(backdrop)
    expect(onCancel).not.toHaveBeenCalled()
    unmount()
  })

  it('gives focus back to the trigger of the menu whose item opened it', () => {
    const root = document.createElement('div')
    root.innerHTML = '<button aria-haspopup="menu">ring</button><div role="menu"><button role="menuitem">Отменить загрузку</button></div>'
    document.body.append(root)
    const trigger = root.querySelector<HTMLElement>('[aria-haspopup]')!
    root.querySelector<HTMLElement>('[role="menuitem"]')!.focus()
    const { unmount } = render(<Sheet title="Отменить загрузку?" onCancel={() => {}} actions={[{ label: 'Не отменять', kind: 'cancel', run: () => {} }]} />)
    root.querySelector('[role="menu"]')!.remove() // the menu's exit is over
    unmount()
    expect(document.activeElement).toBe(trigger)
    root.remove()
  })

  it('keeps a confirmation open on a click outside it', () => {
    const onCancel = vi.fn()
    const { unmount } = render(<Sheet title="Удалить?" onCancel={onCancel} actions={[{ label: 'Отменить', kind: 'cancel', run: () => {} }]} />)
    const backdrop = document.querySelector('[role="alertdialog"]')!.parentElement!
    fireEvent.mouseDown(backdrop)
    fireEvent.click(backdrop)
    expect(onCancel).not.toHaveBeenCalled()
    unmount()
  })

  it('a dialog without a Cancel button closes with the round ✕; a form or a confirmation keeps its Cancel', () => {
    const onCancel = vi.fn()
    const picker = render(<Sheet title="Раздачи" role="dialog" onCancel={onCancel} actions={[{ label: 'Смотреть', kind: 'primary', run: () => {} }]} />)
    const close = screen.getByRole('button', { name: 'Закрыть' })
    // focus on open goes to the content (here the primary button), not to the ✕
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Смотреть' }))
    fireEvent.click(close)
    expect(onCancel).toHaveBeenCalledTimes(1)
    picker.unmount()

    const form = render(<Sheet title="Новый профиль" role="dialog" onCancel={() => {}} actions={[{ label: 'Отменить', kind: 'cancel', run: () => {} }, { label: 'Сохранить', kind: 'primary', run: () => {} }]} />)
    expect(screen.queryByRole('button', { name: 'Закрыть' })).toBeNull()
    form.unmount()
    const alert = render(<Sheet title="Удалить?" onCancel={() => {}} actions={[{ label: 'Удалить', kind: 'destructive', run: () => {} }]} />)
    expect(screen.queryByRole('button', { name: 'Закрыть' })).toBeNull()
    alert.unmount()
  })
})
