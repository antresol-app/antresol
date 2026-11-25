// ui/ToolButton: the rounded-square toolbar button of a bar.
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Icon } from '../../components/Icon'
import * as sdkUi from '../../addons/sdkUi'
import { ToolButton } from '../ToolButton'
import { css, rule } from './cssRule'

describe('ToolButton', () => {
  it('a glyph alone is named and has a tooltip by its label; a tooltip of its own wins', () => {
    const { rerender } = render(<ToolButton icon="search" label="Поиск" />)
    const b = screen.getByRole('button', { name: 'Поиск' })
    expect(b).toHaveAttribute('type', 'button')
    expect(b).toHaveAttribute('title', 'Поиск')
    expect(b.querySelector('svg')).toHaveAttribute('width', '20')
    rerender(<ToolButton icon="search" label="Поиск" tooltip="Найти в книге (F)" size="sm" />)
    expect(screen.getByRole('button', { name: 'Поиск' })).toHaveAttribute('title', 'Найти в книге (F)')
    expect(screen.getByRole('button').querySelector('svg')).toHaveAttribute('width', '16')
    expect(screen.getByRole('button')).toHaveClass('sm')
  })

  it('draws a glyph of the page’s own and a word after it, which needs no tooltip', () => {
    render(
      <ToolButton icon={<Icon name="chevronLeft" size={22} />} label="Книги">
        <span>Книги</span>
      </ToolButton>,
    )
    const b = screen.getByRole('button', { name: 'Книги' })
    expect(b).not.toHaveAttribute('title')
    expect(b.querySelector('svg')).toHaveAttribute('width', '22')
    expect(b).toHaveTextContent('Книги')
  })

  it('a toggle says so (aria-pressed) and shows it; a plain tool has no aria-pressed', () => {
    const { rerender } = render(<ToolButton icon="quote" label="Текст" />)
    expect(screen.getByRole('button')).not.toHaveAttribute('aria-pressed')
    rerender(<ToolButton icon="quote" label="Текст" toggled={false} />)
    expect(screen.getByRole('button')).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByRole('button')).not.toHaveClass('on')
    rerender(<ToolButton icon="quote" label="Текст" toggled />)
    expect(screen.getByRole('button', { pressed: true })).toHaveClass('on')
  })

  it('passes its props on (a menu trigger’s, a data attribute) and a disabled one takes no click', async () => {
    const onClick = vi.fn()
    render(<ToolButton icon="arrowUp" label="Выше" data-dir="up" aria-haspopup="menu" aria-expanded={false} onClick={onClick} disabled />)
    const b = screen.getByRole('button', { name: 'Выше' })
    expect(b).toHaveAttribute('data-dir', 'up')
    expect(b).toHaveAttribute('aria-haspopup', 'menu')
    await userEvent.click(b)
    expect(onClick).not.toHaveBeenCalled()
  })

  it('is handed out to addons with the rest of the kit', () => {
    expect(sdkUi).toHaveProperty('ToolButton')
  })
})

describe('ToolButton’s look', () => {
  it('is 40 pt (36 in a row), 10 pt round, and a bar paints it through --tool-* with the tokens as fallbacks', () => {
    const f = 'ui/ToolButton.module.css'
    expect(rule(f, '.tool')).toContain('--h: var(--tool-h, var(--control-h-tool))')
    expect(rule(f, '.tool')).toContain('border-radius: var(--r-field)')
    expect(rule(f, '.tool')).toContain('color: var(--tool-color, var(--label-2))')
    expect(rule(f, '.sm')).toContain('--h: var(--tool-h, var(--control-h-sm))')
    expect(css(f)).toContain('var(--tool-hover-fill, var(--fill-3))')
    expect(css(f)).toContain('var(--tool-on-fill, var(--label))')
    expect(css('design/tokens.css')).toMatch(/--control-h-tool:\s*40px;/)
  })

  it('grows a finger’s hit area, not the square, on a touch screen', () => {
    const c = css('ui/ToolButton.module.css')
    const touch = c.slice(c.indexOf('@media (pointer: coarse)'))
    expect(touch).toContain('var(--touch-target)')
    expect(c.slice(0, c.indexOf('@media (pointer: coarse)'))).not.toContain('--touch-target')
  })
})
