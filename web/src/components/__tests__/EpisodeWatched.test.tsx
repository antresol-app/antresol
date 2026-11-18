// A watched episode tile is dimmed with the eye, as a card is (web/DESIGN.md,
// «A watched title»); one in progress keeps its bar.
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { Episode } from '../../api/types'
import { EpisodeList } from '../EpisodeList'

const ep = (n: number, over: Partial<Episode> = {}): Episode => ({
  id: n, kind: 'episode', title: `Серия ${n}`, subtitle: '', poster: 'e.jpg', backdrop: '', logo: '', progress: 0, onServer: true,
  seasonNo: 1, episodeNo: n, watched: false, ...over,
})

const draw = (episodes: Episode[]) =>
  render(<EpisodeList seasons={[{ id: 1, number: 1, title: '', episodes }]} onPlay={() => {}} />)

describe('episode tiles', () => {
  it('a watched episode: dimmed still, the eye, no bar', () => {
    const { container } = draw([ep(1, { watched: true, progress: 1 })])
    expect(container.querySelector('img')).toHaveClass('dim')
    expect(screen.getByRole('img', { name: 'Просмотрено' })).toBeInTheDocument()
    expect(container.querySelector('[aria-hidden] > div[style]')).toBeNull()
  })

  it('an episode in progress: the bar, the picture as it is', () => {
    const { container } = draw([ep(2, { progress: 0.5 })])
    expect(container.querySelector('img')).not.toHaveClass('dim')
    expect(screen.queryByRole('img', { name: 'Просмотрено' })).not.toBeInTheDocument()
    expect(container.querySelector('[aria-hidden] > div[style]')).not.toBeNull()
  })
})
