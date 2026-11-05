// The one menu: in a portal (no card clips it or grows with it), always
// inside the window — between the top bar and the tab bar — on the side
// where it fits, else the roomier one, shortened, the choice in view.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { Menu, MenuItem } from '../Menu'
import { Sheet } from '../Sheet'

const rect = (top: number, height: number, left = 600, width = 120) =>
  ({ top, bottom: top + height, height, left, right: left + width, width, x: left, y: top, toJSON: () => ({}) }) as DOMRect

function Demo({ items = ['Пункт'], checked }: { items?: string[]; checked?: string }) {
  return (
    // a card that clips its content, like a settings group
    <div data-testid="card" style={{ overflow: 'hidden' }}>
      <Menu label="Меню" trigger={(p) => <button {...p}>Открыть</button>}>
        {() => items.map((x) => <MenuItem key={x} label={x} checked={checked === undefined ? undefined : x === checked} />)}
      </Menu>
    </div>
  )
}

describe('Menu placement', () => {
  let triggerTop = 0
  let menuHeight = 200
  const bars: HTMLElement[] = []
  const addBar = (attr: 'bottomBar' | 'topBar') => {
    const bar = document.createElement('nav')
    bar.dataset[attr] = ''
    document.body.append(bar)
    bars.push(bar)
  }
  beforeEach(() => {
    menuHeight = 200
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function (this: HTMLElement) {
      return this.getAttribute('role') === 'menu' ? menuHeight : this.getAttribute('role')?.startsWith('menuitem') ? 40 : 0
    })
    vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(function (this: HTMLElement) {
      return this.getAttribute('role') === 'menu' ? 260 : 0
    })
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      if (this.dataset.bottomBar !== undefined) return rect(760, 84, 0, 1440)
      if (this.dataset.topBar !== undefined) return rect(0, 76, 0, 1440)
      if (this.tagName === 'BUTTON' && this.getAttribute('aria-haspopup')) return rect(triggerTop, 36)
      return rect(0, 0)
    })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 844 })
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 })
  })
  afterEach(() => {
    for (const b of bars.splice(0)) b.remove()
    vi.restoreAllMocks()
  })
  const open = async () => {
    await userEvent.click(screen.getByRole('button', { name: 'Открыть' }))
    return screen.getByRole('menu')
  }

  it('renders in a portal, outside the card that clips it', async () => {
    triggerTop = 100
    render(<Demo />)
    const menu = await open()
    expect(screen.getByTestId('card').contains(menu)).toBe(false)
    expect(menu.parentElement).toBe(document.body)
  })

  it('opens below the trigger when it fits', async () => {
    triggerTop = 100
    render(<Demo />)
    const menu = await open()
    expect(menu.dataset.side).toBe('bottom')
    expect(menu.style.top).toBe(`${136 + 8}px`)
  })

  it('opens above when it would go under the tab bar', async () => {
    addBar('bottomBar')
    triggerTop = 640
    render(<Demo />)
    const menu = await open()
    expect(menu.dataset.side).toBe('top')
    expect(menu.style.top).toBe(`${640 - 8 - 200}px`)
  })

  it('fits on neither side: the roomier one, shortened, never over the top bar', async () => {
    addBar('topBar')
    addBar('bottomBar')
    menuHeight = 700
    triggerTop = 500 // above: 500 − 76 − 16 = 408; below: 760 − 536 − 16 = 208
    render(<Demo />)
    const menu = await open()
    expect(menu.dataset.side).toBe('top')
    expect(menu.style.maxHeight).toBe('408px')
    expect(menu.style.top).toBe(`${76 + 8}px`)
  })

  it('more room below: down, shortened to the tab bar', async () => {
    addBar('topBar')
    addBar('bottomBar')
    menuHeight = 700
    triggerTop = 200 // above: 108; below: 760 − 236 − 16 = 508
    render(<Demo />)
    const menu = await open()
    expect(menu.dataset.side).toBe('bottom')
    expect(menu.style.maxHeight).toBe('508px')
  })

  it('scrolls the chosen item into view when the menu scrolls', async () => {
    addBar('bottomBar')
    menuHeight = 600
    triggerTop = 400
    vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(function (this: HTMLElement) {
      return this.getAttribute('role') === 'menu' ? 480 : 0
    })
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(function (this: HTMLElement) {
      return this.getAttribute('role') === 'menu' ? 200 : 0
    })
    vi.spyOn(HTMLElement.prototype, 'offsetTop', 'get').mockImplementation(function (this: HTMLElement) {
      return this.getAttribute('aria-checked') === 'true' ? 440 : 0
    })
    const items = Array.from({ length: 12 }, (_, i) => `Пункт ${i + 1}`)
    render(<Demo items={items} checked="Пункт 12" />)
    const menu = await open()
    expect(menu.scrollTop).toBeGreaterThan(0)
  })

  it('follows its trigger when the page scrolls', async () => {
    triggerTop = 100
    const raf = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => {
      cb(0)
      return 0
    })
    render(<Demo />)
    const menu = await open()
    triggerTop = 60
    act(() => {
      fireEvent.scroll(window)
    })
    expect(menu.style.top).toBe(`${96 + 8}px`)
    raf.mockRestore()
  })

  it('inside a Sheet the bars do not count (the sheet covers them)', async () => {
    addBar('bottomBar')
    triggerTop = 600 // to the bar: 760 − 636 − 16 = 108 (would flip); to the window: 844 − 636 − 16 = 192
    menuHeight = 150
    render(
      <Sheet title="Окно" role="dialog" onCancel={() => {}} actions={[]}>
        <Demo />
      </Sheet>,
    )
    const menu = await open()
    expect(menu.dataset.side).toBe('bottom')
  })

  describe('phones (the menu is a sheet across the bottom)', () => {
    beforeEach(() => {
      addBar('bottomBar')
      vi.stubGlobal('matchMedia', (q: string) => ({ matches: q.includes('max-width'), media: q, addEventListener() {}, removeEventListener() {} }))
    })
    afterEach(() => vi.unstubAllGlobals())

    it('room under the trigger: right under it', async () => {
      triggerTop = 100
      render(<Demo />)
      const menu = await open()
      expect(menu.style.top).toBe(`${136 + 8}px`)
      expect(menu.style.bottom).toBe('auto')
    })

    it('trigger near the tab bar: right over the trigger', async () => {
      triggerTop = 600
      render(<Demo />)
      const menu = await open()
      expect(menu.style.bottom).toBe(`${844 - 600 + 8}px`)
    })

    it('room on neither side: stands on the tab bar, shortened', async () => {
      menuHeight = 700
      triggerTop = 400
      render(<Demo />)
      const menu = await open()
      expect(menu.style.bottom).toBe(`${844 - 760 + 8}px`)
      expect(menu.style.maxHeight).toBe(`${844 * 0.7}px`)
    })
  })
})

describe('Menu focus', () => {
  function WithSheet() {
    const [sheet, setSheet] = useState(false)
    return (
      <>
        <Menu label="Меню" trigger={(p) => <button {...p}>Ещё</button>}>
          {(close) => (
            <MenuItem
              label="Удалить…"
              onSelect={() => {
                close(false)
                setSheet(true)
              }}
            />
          )}
        </Menu>
        {sheet && <Sheet title="Удалить?" onCancel={() => setSheet(false)} actions={[{ label: 'Отменить', kind: 'cancel', run: () => setSheet(false) }]} />}
      </>
    )
  }

  it('a sheet opened from an item gives the focus back to the trigger', async () => {
    render(<WithSheet />)
    await userEvent.click(screen.getByRole('button', { name: 'Ещё' }))
    await userEvent.click(screen.getByRole('menuitem', { name: 'Удалить…' }))
    await userEvent.click(screen.getByRole('button', { name: 'Отменить' }))
    expect(screen.getByRole('button', { name: 'Ещё' })).toHaveFocus()
  })

  it('Tab closes it and gives the focus back to the trigger, so the next Tab goes on from there', async () => {
    render(
      <>
        <Menu label="Меню" trigger={(p) => <button {...p}>Открыть</button>}>
          {() => <MenuItem label="Пункт" />}
        </Menu>
        <button type="button">Дальше</button>
      </>,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Открыть' }))
    expect(screen.getByRole('menuitem')).toHaveFocus()
    await userEvent.tab()
    expect(screen.getByRole('button', { name: 'Открыть' })).toHaveFocus()
    await userEvent.tab()
    expect(screen.getByRole('button', { name: 'Дальше' })).toHaveFocus()
  })
})

describe('Menu layers', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('inside a layer of its own (the player, not full screen) it renders in that layer, above its chrome', async () => {
    render(
      <div data-portal-host data-testid="player" style={{ position: 'fixed', inset: 0, zIndex: 200 }}>
        <Menu label="Качество" trigger={(p) => <button {...p}>Качество</button>}>
          {() => <MenuItem label="1080p" />}
        </Menu>
      </div>,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Качество' }))
    expect(screen.getByRole('menu').parentElement).toBe(screen.getByTestId('player'))
  })

  it('places itself again when its content changes size (a filtered list)', async () => {
    let height = 400
    let onResize: (() => void) | undefined
    vi.stubGlobal(
      'ResizeObserver',
      class {
        constructor(cb: () => void) {
          onResize = cb
        }
        observe() {}
        disconnect() {}
      },
    )
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function (this: HTMLElement) {
      return this.getAttribute('role') === 'menu' ? height : 0
    })
    vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(() => 260)
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      return this.getAttribute('aria-haspopup') ? rect(780, 36) : rect(0, 0)
    })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 844 })
    render(
      <Menu label="Студии" trigger={(p) => <button {...p}>Добавить студию</button>}>
        {() => <MenuItem label="LostFilm" />}
      </Menu>,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Добавить студию' }))
    const menu = screen.getByRole('menu')
    expect(menu.style.top).toBe(`${780 - 8 - 400}px`)
    height = 60
    act(() => onResize?.())
    // still right above its trigger, not floating where the long list began
    expect(menu.style.top).toBe(`${780 - 8 - 60}px`)
  })
})
