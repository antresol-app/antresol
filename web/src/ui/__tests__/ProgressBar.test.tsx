// The bar moves on the compositor: the fill is a full-width strip shifted
// left by what is still to go (a transform), never a width that would lay
// out the row on every frame of its transition («Загрузки» moves every 2 s).
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ProgressBar } from '../ProgressBar'

const fillOf = (bar: HTMLElement) => bar.firstElementChild as HTMLElement

describe('ProgressBar', () => {
  it('draws 60 % as the fill shifted 40 % to the left, with no width of its own', () => {
    render(<ProgressBar value={0.6} label="Загрузка" />)
    const bar = screen.getByRole('progressbar')
    expect(bar).toHaveAttribute('aria-valuenow', '60')
    expect(fillOf(bar).style.transform).toBe('translateX(-40%)')
    expect(fillOf(bar).style.width).toBe('')
  })

  it('clamps to 0…100 %', () => {
    const { container, rerender } = render(<ProgressBar value={-1} />)
    const fill = () => fillOf(container.firstElementChild as HTMLElement)
    expect(fill().style.transform).toBe('translateX(-100%)')
    rerender(<ProgressBar value={2} />)
    expect(fill().style.transform).toBe('translateX(0%)')
  })
})
