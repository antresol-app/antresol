import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { Toast } from '../Toast'

describe('Toast', () => {
  it('keeps its live region mounted and empty while there is nothing to say', () => {
    render(<Toast>{null}</Toast>)
    const region = screen.getByRole('status')
    expect(region).toBeEmptyDOMElement()
    expect(region).toHaveAttribute('aria-live', 'polite')
  })

  it('shows the message inside the region, in a portal on <body>', () => {
    const { container } = render(<Toast>Добавлено в очередь</Toast>)
    expect(screen.getByRole('status')).toHaveTextContent('Добавлено в очередь')
    expect(container).toBeEmptyDOMElement()
    expect(screen.getByRole('status').parentElement).toBe(document.body)
  })

  it('offers one action that undoes what happened', () => {
    const undo = vi.fn()
    render(<Toast action={{ label: 'Отменить', onClick: undo }}>Больше не следите</Toast>)
    fireEvent.click(screen.getByRole('button', { name: 'Отменить' }))
    expect(undo).toHaveBeenCalledTimes(1)
  })

  it('without `live` it is hidden from assistive tech (another region reads the words)', () => {
    render(<Toast live={false}>Нет сети</Toast>)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(screen.getByTestId('toast-region')).toHaveAttribute('aria-hidden', 'true')
  })

  it('an error is an alert, in the warning tone', () => {
    render(<Toast tone="error">Не удалось добавить</Toast>)
    const region = screen.getByRole('alert')
    expect(region).toHaveTextContent('Не удалось добавить')
    expect(region.firstElementChild).toHaveAttribute('data-tone', 'error')
  })
})
