// The horizontal rail of cards: on a pointer device chevrons page through it,
// each hidden at its own end. Home's shelves and a series' episodes share it.
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { EpisodeList } from '../EpisodeList'
import { Rail } from '../Rail'

/** Gives the rail's list a scroll box: jsdom lays nothing out. */
function scrollBox(list: HTMLElement, { client, scroll, left }: { client: number; scroll: number; left: number }) {
  Object.defineProperty(list, 'clientWidth', { configurable: true, value: client })
  Object.defineProperty(list, 'scrollWidth', { configurable: true, value: scroll })
  list.scrollLeft = left
  fireEvent.scroll(list)
}

/** The chevrons are pointer-only, kept out of the accessibility tree: found by their side. */
const chevron = (dir: 'prev' | 'next') => document.querySelector<HTMLButtonElement>(`[data-dir="${dir}"]`)!
const shown = (dir: 'prev' | 'next') => !chevron(dir).hidden

describe('Rail', () => {
  it('shows only the chevrons that lead somewhere and pages by most of a screen', () => {
    render(
      <Rail label="Сезон 2">
        {[1, 2, 3].map((i) => (
          <li key={i}>{i}</li>
        ))}
      </Rail>,
    )
    const list = screen.getByRole('list', { name: 'Сезон 2' })
    list.scrollBy = vi.fn()
    // screen readers and the keyboard go through the cells, never the chevrons
    expect(screen.queryByRole('button')).toBeNull()
    expect(chevron('next')).toHaveAttribute('aria-hidden', 'true')
    // nothing to scroll: no chevrons
    expect(shown('prev')).toBe(false)
    expect(shown('next')).toBe(false)

    scrollBox(list, { client: 1000, scroll: 3000, left: 0 })
    expect(shown('prev')).toBe(false)
    fireEvent.click(chevron('next'))
    expect(list.scrollBy).toHaveBeenCalledWith(expect.objectContaining({ left: 850 }))

    scrollBox(list, { client: 1000, scroll: 3000, left: 1000 })
    expect(shown('prev')).toBe(true)
    expect(shown('next')).toBe(true)

    scrollBox(list, { client: 1000, scroll: 3000, left: 2000 })
    expect(shown('next')).toBe(false)
    fireEvent.click(chevron('prev'))
    expect(list.scrollBy).toHaveBeenLastCalledWith(expect.objectContaining({ left: -850 }))
  })

  it('the episode row of a season is such a rail', () => {
    const ep = (n: number) => ({
      id: n, kind: 'episode' as const, title: `Серия ${n}`, subtitle: '', poster: '', backdrop: '', logo: '', progress: 0, onServer: true,
      seasonNo: 2, episodeNo: n, watched: false,
    })
    render(<EpisodeList seasons={[{ id: 1, number: 2, title: '', episodes: [1, 2, 3, 4, 5, 6].map(ep) }]} onPlay={() => {}} />)
    const list = screen.getByRole('list', { name: 'Сезон 2' })
    scrollBox(list, { client: 1000, scroll: 2400, left: 0 })
    expect(shown('next')).toBe(true)
    expect(shown('prev')).toBe(false)
  })
})
