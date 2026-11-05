// The progress ring of a download card: one size and one stroke whatever the
// state — downloading, paused, failed or done (a check in place of the percent).
import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { StatusRing, type RingState } from '../StatusRing'

const geometry = (svg: SVGSVGElement) => ({
  width: svg.getAttribute('width'),
  height: svg.getAttribute('height'),
  strokes: [...svg.querySelectorAll('circle')].map((c) => c.getAttribute('stroke-width')),
  radii: [...svg.querySelectorAll('circle')].map((c) => c.getAttribute('r')),
})

describe('StatusRing', () => {
  it('has the same size and stroke in every state', () => {
    const states: [RingState, number][] = [
      ['active', 0.05],
      ['active', 0.99],
      ['paused', 0.35],
      ['error', 0],
      ['done', 1],
    ]
    const shapes = states.map(([state, value]) => {
      const { container, unmount } = render(<StatusRing state={state} value={value} />)
      const g = geometry(container.querySelector('svg')!)
      unmount()
      return g
    })
    for (const s of shapes) expect(s).toEqual(shapes[0])
  })

  it('shows the percent while it runs and a check, not 100 %, when done', () => {
    const { container, rerender } = render(<StatusRing state="active" value={0.994} />)
    expect(container).toHaveTextContent('99%')
    expect(container.querySelector('[data-icon="check"]')).toBeNull()
    rerender(<StatusRing state="done" value={1} />)
    expect(container).not.toHaveTextContent('%')
    expect(container.querySelector('[data-icon="check"]')).not.toBeNull()
  })

  it('a failed download shows «!», not a 0 % that looks like one waiting', () => {
    const { container } = render(<StatusRing state="error" value={0} />)
    expect(container).toHaveTextContent('!')
    expect(container).not.toHaveTextContent('%')
  })

  it('the arc covers the value: 99 % leaves a gap, done closes it', () => {
    const { container, rerender } = render(<StatusRing state="active" value={0.99} />)
    const arc = () => container.querySelector('[data-arc]')!
    expect(arc().getAttribute('stroke-dasharray')).toBe('99 1')
    rerender(<StatusRing state="done" value={0.5} />)
    expect(arc().getAttribute('stroke-dasharray')).toBe('100 0')
  })
})
