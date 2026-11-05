// The popup button that replaces native <select>s: a pill with the current
// choice that opens the app's menu — radio items for one choice, checkbox
// items (the menu stays open) for several.
import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MultiSelect, Select } from '../Select'

const modes = [
  { value: 'off', label: 'Выключены' },
  { value: 'foreign', label: 'Если звук не на моём языке' },
  { value: 'always', label: 'Всегда' },
]

describe('Select', () => {
  it('names the setting and the choice, opens a menu with the choice checked and picks another', async () => {
    const onChange = vi.fn()
    render(<Select label="Субтитры" value="off" options={modes} onChange={onChange} />)
    const trigger = screen.getByRole('button', { name: 'Субтитры: Выключены' })
    expect(trigger).toHaveAttribute('aria-haspopup', 'menu')
    expect(trigger).toHaveTextContent('Выключены')
    await userEvent.click(trigger)
    const items = screen.getAllByRole('menuitemradio')
    expect(items.map((i) => i.textContent)).toEqual(['Выключены', 'Если звук не на моём языке', 'Всегда'])
    expect(items[0]).toHaveAttribute('aria-checked', 'true')
    expect(items[0]).toHaveFocus()
    await userEvent.click(items[1]!)
    expect(onChange).toHaveBeenCalledWith('foreign')
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
  })

  it('works from the keyboard: arrows move, Enter picks, Escape gives the focus back', async () => {
    const onChange = vi.fn()
    render(<Select label="Субтитры" value="off" options={modes} onChange={onChange} />)
    const trigger = screen.getByRole('button', { name: 'Субтитры: Выключены' })
    trigger.focus()
    await userEvent.keyboard('{Enter}')
    await userEvent.keyboard('{ArrowDown}{ArrowDown}{Enter}')
    expect(onChange).toHaveBeenCalledWith('always')
    await userEvent.keyboard('{Enter}')
    expect(screen.getByRole('menu')).toBeInTheDocument()
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    expect(trigger).toHaveFocus()
  })

  it('opens on ↓ or ↑ like a pop-up button', async () => {
    render(<Select label="Субтитры" value="always" options={modes} onChange={() => {}} />)
    screen.getByRole('button', { name: 'Субтитры: Всегда' }).focus()
    await userEvent.keyboard('{ArrowDown}')
    expect(screen.getByRole('menuitemradio', { name: 'Всегда' })).toHaveFocus()
  })

  it('a value that is none of the options reads «Не выбрано», not an empty pill', () => {
    render(<Select label="Категория" value="" options={modes} onChange={() => {}} />)
    expect(screen.getByRole('button', { name: 'Категория: Не выбрано' })).toHaveTextContent('Не выбрано')
  })

  it('can show its label inside the pill', () => {
    render(<Select label="Сезон" showLabel value="2" options={[{ value: '2', label: '2' }]} onChange={() => {}} />)
    expect(screen.getByRole('button', { name: 'Сезон: 2' })).toHaveTextContent('Сезон2')
  })
})

describe('MultiSelect', () => {
  const langs = [
    { value: 'ru', label: 'Русский' },
    { value: 'en', label: 'Английский' },
  ]

  it('lists the chosen ones, toggles with checkboxes and keeps the menu open', async () => {
    const onChange = vi.fn()
    const { rerender } = render(<MultiSelect label="Языки субтитров" value={['ru']} options={langs} none="Никакие" onChange={onChange} />)
    await userEvent.click(screen.getByRole('button', { name: 'Языки субтитров: Русский' }))
    const items = screen.getAllByRole('menuitemcheckbox')
    expect(items.map((i) => i.getAttribute('aria-checked'))).toEqual(['true', 'false'])
    await userEvent.click(items[1]!)
    expect(onChange).toHaveBeenLastCalledWith(['ru', 'en'])
    expect(screen.getByRole('menu')).toBeInTheDocument()
    rerender(<MultiSelect label="Языки субтитров" value={[]} options={langs} none="Никакие" onChange={onChange} />)
    expect(screen.getByRole('button', { name: 'Языки субтитров: Никакие' })).toBeInTheDocument()
  })

  it('keeps at least `min` chosen', async () => {
    const onChange = vi.fn()
    render(<MultiSelect label="Языки" value={['ru']} min={1} options={langs} onChange={onChange} />)
    await userEvent.click(screen.getByRole('button', { name: 'Языки: Русский' }))
    const ru = screen.getByRole('menuitemcheckbox', { name: 'Русский' })
    expect(ru).toHaveAttribute('aria-disabled', 'true')
    await userEvent.click(ru)
    expect(onChange).not.toHaveBeenCalled()
  })
})
