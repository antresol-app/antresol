import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { PlayerBar, PlayerBarButton } from '../PlayerBar'

const base = { label: 'Сейчас играет', title: 'Child in Time', subtitle: 'Deep Purple', fallbackIcon: 'music' as const, progress: 0.25 }

describe('PlayerBar', () => {
  it('is a named landmark whose text opens the full player', () => {
    const open = vi.fn()
    render(
      <PlayerBar {...base} onOpen={open} openLabel="Открыть: Child in Time, Deep Purple">
        <PlayerBarButton icon="play" label="Играть" onClick={() => {}} prominent />
      </PlayerBar>,
    )
    expect(screen.getByRole('complementary', { name: 'Сейчас играет' })).toHaveAttribute('data-player-bar')
    fireEvent.click(screen.getByRole('button', { name: 'Открыть: Child in Time, Deep Purple' }))
    expect(open).toHaveBeenCalledTimes(1)
  })

  it('without onOpen the text is not a button', () => {
    render(
      <PlayerBar {...base}>
        <PlayerBarButton icon="play" label="Играть" onClick={() => {}} />
      </PlayerBar>,
    )
    expect(screen.getAllByRole('button')).toHaveLength(1)
    expect(screen.getByText('Child in Time')).toBeInTheDocument()
  })

  it('a control is a named button that reports busy', () => {
    const toggle = vi.fn()
    render(
      <PlayerBar {...base}>
        <PlayerBarButton icon="pause" label="Пауза" onClick={toggle} busy />
      </PlayerBar>,
    )
    const b = screen.getByRole('button', { name: 'Пауза' })
    expect(b).toHaveAttribute('aria-busy', 'true')
    fireEvent.click(b)
    expect(toggle).toHaveBeenCalled()
  })

  it('shows the glyph instead of artwork that fails to load', () => {
    const { container } = render(
      <PlayerBar {...base} artwork="/api/images/x.jpg">
        <PlayerBarButton icon="play" label="Играть" onClick={() => {}} />
      </PlayerBar>,
    )
    const img = container.querySelector('img')!
    expect(img).toHaveAttribute('src', '/api/images/x.jpg')
    fireEvent.error(img)
    expect(container.querySelector('img')).toBeNull()
  })

  it('draws the progress clamped to the bar', () => {
    const { container, rerender } = render(
      <PlayerBar {...base} progress={1.7}>
        <PlayerBarButton icon="play" label="Играть" onClick={() => {}} />
      </PlayerBar>,
    )
    const fill = () => container.querySelector<HTMLElement>('[aria-hidden] > span')!
    expect(fill().style.width).toBe('100%')
    rerender(
      <PlayerBar {...base} progress={0.5}>
        <PlayerBarButton icon="play" label="Играть" onClick={() => {}} />
      </PlayerBar>,
    )
    expect(fill().style.width).toBe('50%')
  })
})
