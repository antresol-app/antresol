import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { NumberField, parseNumber } from '../NumberField'

describe('parseNumber', () => {
  it('reads a comma or a point, NaN for empty or partial input', () => {
    expect(parseNumber('2')).toBe(2)
    expect(parseNumber('0,5')).toBe(0.5)
    expect(parseNumber(' 1.5 ')).toBe(1.5)
    expect(parseNumber('')).toBeNaN()
    expect(parseNumber(',')).toBeNaN()
  })
})

function Field({ initial = 2, onChange = () => {}, ...rest }: { initial?: number; onChange?: (v: number) => void } & Partial<Parameters<typeof NumberField>[0]>) {
  const [v, setV] = useState(initial)
  return (
    <NumberField
      label="Upload limit"
      unit="MB/s"
      value={v}
      onChange={(n) => {
        setV(n)
        onChange(n)
      }}
      {...rest}
    />
  )
}

describe('NumberField', () => {
  it('shows the value and its unit, named by its label', () => {
    render(<Field />)
    const input = screen.getByRole('textbox', { name: 'Upload limit' })
    expect(input).toHaveValue('2')
    expect(screen.getByText('MB/s')).toBeInTheDocument()
  })

  it('commits what is typed when it is valid (a comma is a decimal point)', async () => {
    const onChange = vi.fn()
    render(<Field decimals={2} onChange={onChange} />)
    const input = screen.getByRole('textbox')
    await userEvent.clear(input)
    await userEvent.type(input, '0,5')
    expect(onChange).toHaveBeenLastCalledWith(0.5)
  })

  it('ignores letters and puts the saved value back when left invalid', async () => {
    const onChange = vi.fn()
    render(<Field onChange={onChange} min={1} />)
    const input = screen.getByRole('textbox')
    await userEvent.clear(input)
    await userEvent.type(input, 'x0')
    expect(input).toHaveValue('0')
    expect(onChange).not.toHaveBeenCalled()
    await userEvent.tab()
    expect(input).toHaveValue('2')
  })

  it('a whole-number field takes no decimals', async () => {
    render(<Field initial={10} />)
    const input = screen.getByRole('textbox')
    await userEvent.type(input, ',')
    expect(input).toHaveValue('10')
  })

  it('follows the value from outside while not being edited', () => {
    const { rerender } = render(<NumberField label="Cache" unit="GB" value={10} onChange={() => {}} />)
    rerender(<NumberField label="Cache" unit="GB" value={20} onChange={() => {}} />)
    expect(screen.getByRole('textbox')).toHaveValue('20')
  })

  it('is as wide as what it shows, in digits, never narrower than `chars`', async () => {
    const width = () => (screen.getByRole('textbox').closest('[style]') as HTMLElement).style.getPropertyValue('--nf-chars')
    const { unmount } = render(<Field initial={2} decimals={2} />)
    expect(width()).toBe('1')
    const input = screen.getByRole('textbox')
    await userEvent.clear(input)
    expect(width()).toBe('1')
    await userEvent.type(input, '12,75')
    expect(width()).toBe('5')
    unmount()
    render(<Field initial={10} chars={3} />)
    expect(width()).toBe('3')
    expect((screen.getByRole('textbox').closest('[style]') as HTMLElement).style.getPropertyValue('--nf-min')).toBe('3')
  })

  it('keeps the number and its unit in one line of text, so they share a baseline', () => {
    render(<NumberField label="Cache" unit="GB" value={10} onChange={() => {}} />)
    const input = screen.getByRole('textbox')
    // one inline run of text inside the field (not two flex items centred apart)
    const line = input.parentElement!
    expect(screen.getByText('GB').parentElement).toBe(line)
    expect(line).not.toBe(input.closest('[style]'))
    expect(line.tagName).toBe('SPAN')
  })

  it('a press anywhere on the field (its unit, the finger’s margin around it) puts the caret in the number', () => {
    render(<NumberField label="Скорость" unit="МБ/с" value={2} onChange={() => {}} />)
    const input = screen.getByRole('textbox', { name: 'Скорость' })
    fireEvent.mouseDown(screen.getByText('МБ/с').parentElement!)
    expect(document.activeElement).toBe(input)
  })
})
