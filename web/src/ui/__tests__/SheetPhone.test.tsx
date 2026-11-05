import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { Sheet } from '../Sheet'
import { setViewport } from '../../test/setup'

const confirm = <Sheet title="Удалить?" onCancel={() => {}} actions={[{ label: 'Отменить', kind: 'cancel', run: () => {} }]} />

describe('Sheet on a phone', () => {
  afterEach(() => setViewport(1440))

  it('is a bottom sheet on a phone and a centred one on a wide screen', () => {
    setViewport(390)
    const phone = render(confirm)
    expect(document.querySelector('[role="alertdialog"]')).toHaveAttribute('data-placement', 'bottom')
    phone.unmount()
    setViewport(1440)
    render(confirm)
    expect(document.querySelector('[role="alertdialog"]')).toHaveAttribute('data-placement', 'center')
  })

  it('tracks the on-screen keyboard while open, so its fields stay above it', () => {
    const { unmount } = render(confirm)
    expect(document.documentElement.style.getPropertyValue('--keyboard-inset')).toBe('0px')
    unmount()
    expect(document.documentElement.style.getPropertyValue('--keyboard-inset')).toBe('')
  })

  it('a form scrolls in its own body: the title and the buttons stay pinned (above the keyboard)', () => {
    setViewport(390)
    render(
      <Sheet
        title="Изменить профиль"
        role="dialog"
        wide
        onCancel={() => {}}
        actions={[
          { label: 'Отмена', kind: 'cancel', run: () => {} },
          { label: 'Сохранить', kind: 'primary', run: () => {} },
        ]}
      >
        <input aria-label="Имя" />
      </Sheet>,
    )
    const dialog = document.querySelector('[role="dialog"]')!
    const body = dialog.querySelector('[data-sheet-body]')!
    expect(body).toContainElement(document.querySelector('input'))
    const save = document.querySelector<HTMLElement>('[data-primary="true"]')!
    expect(body).not.toContainElement(save)
    expect(body).not.toContainElement(dialog.querySelector('h2'))
  })

  it('a picker has a grabber: a swipe down closes it, after it slides away', async () => {
    setViewport(390)
    const onCancel = vi.fn()
    render(<Sheet title="Раздачи" role="dialog" dismissOnOutside onCancel={onCancel} />)
    const grabber = screen.getByTestId('sheet-grabber')
    fireEvent.pointerDown(grabber, { pointerId: 1, pointerType: 'touch', clientX: 200, clientY: 300 })
    fireEvent.pointerMove(grabber, { pointerId: 1, pointerType: 'touch', clientX: 200, clientY: 420 })
    fireEvent.pointerMove(grabber, { pointerId: 1, pointerType: 'touch', clientX: 200, clientY: 520 })
    fireEvent.pointerUp(grabber, { pointerId: 1, pointerType: 'touch', clientX: 200, clientY: 520 })
    expect(document.querySelector('[role="dialog"]')).toHaveAttribute('data-closing', 'true')
    await waitFor(() => expect(onCancel).toHaveBeenCalledTimes(1))
  })

  it('a short, slow drag down springs back and keeps it open', async () => {
    setViewport(390)
    // events 80 ms apart (an event's timeStamp is read from Date.now() here)
    let now = 1000
    vi.spyOn(Date, 'now').mockImplementation(() => (now += 80))
    const onCancel = vi.fn()
    render(<Sheet title="Раздачи" role="dialog" dismissOnOutside onCancel={onCancel} />)
    const grabber = screen.getByTestId('sheet-grabber')
    fireEvent.pointerDown(grabber, { pointerId: 1, pointerType: 'touch', clientX: 200, clientY: 300 })
    fireEvent.pointerMove(grabber, { pointerId: 1, pointerType: 'touch', clientX: 200, clientY: 330 })
    fireEvent.pointerUp(grabber, { pointerId: 1, pointerType: 'touch', clientX: 200, clientY: 330 })
    await new Promise((r) => setTimeout(r, 400))
    expect(onCancel).not.toHaveBeenCalled()
  })

  it('a form or a confirmation has no grabber (what was typed is not thrown away by a swipe)', () => {
    setViewport(390)
    render(confirm)
    expect(screen.queryByTestId('sheet-grabber')).toBeNull()
  })

  it('on a phone the ✕ closes it after it slides down, not by vanishing', async () => {
    setViewport(390)
    const onCancel = vi.fn()
    render(<Sheet title="Раздачи" role="dialog" dismissOnOutside onCancel={onCancel} />)
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть' }))
    expect(document.querySelector('[role="dialog"]')).toHaveAttribute('data-closing', 'true')
    expect(onCancel).not.toHaveBeenCalled()
    await waitFor(() => expect(onCancel).toHaveBeenCalledTimes(1))
  })

  it('with the keyboard up an open sheet gets its height (--keyboard-inset), so it stands above it', () => {
    const original = window.visualViewport
    const vv = Object.assign(new EventTarget(), { height: 500, offsetTop: 0 })
    Object.defineProperty(window, 'visualViewport', { configurable: true, value: vv })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 844 })
    try {
      setViewport(390)
      const { unmount } = render(confirm)
      expect(document.documentElement.style.getPropertyValue('--keyboard-inset')).toBe('344px')
      unmount()
    } finally {
      Object.defineProperty(window, 'visualViewport', { configurable: true, value: original })
    }
  })
})
