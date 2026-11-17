// The player page names what the addons' track actions are of: a movie
// itself, an episode its series.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { TrackActionItem } from '../../addons/registry'
import { session } from '../../state/session'

const seen: (TrackActionItem | undefined)[] = []
vi.mock('../../player/VideoPlayer', () => ({
  VideoPlayer: (p: { trackActionItem?: TrackActionItem }) => {
    seen.push(p.trackActionItem)
    return null
  },
}))

import { Player } from '../Player'

const adult = { id: 2, name: 'Мама', avatar: 'grad-1', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const base = { subtitle: '', poster: '', backdrop: '', logo: '', progress: 0, onServer: true, overview: '', genres: [], cast: [], inWatchlist: false, actions: { play: true, toDevice: false, toServer: false, watchNow: false } }
const file = { id: 5, name: 'e1.mkv', size: 1, durationMs: 60000, audio: [], subs: [] }
const items: Record<number, object> = {
  7: { ...base, id: 7, kind: 'movie', title: 'Дюна', files: [file], seasons: [] },
  41: { ...base, id: 41, kind: 'episode', title: 'Пилот', subtitle: 'Миллиарды · S1E1', seriesId: 40, seasonNo: 1, episodeNo: 1, files: [file], seasons: [] },
  40: { ...base, id: 40, kind: 'series', title: 'Миллиарды', files: [], seasons: [] },
}

async function play(id: number) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/login')) return json(adult)
      const m = /\/api\/items\/(\d+)$/.exec(url)
      if (m && items[Number(m[1])]) return json(items[Number(m[1])])
      return json({})
    }),
  )
  await session.login(adult.id)
  render(
    <MemoryRouter initialEntries={[`/watch/${id}`]}>
      <Routes>
        <Route path="/watch/:id" element={<Player />} />
      </Routes>
    </MemoryRouter>,
  )
  await waitFor(() => expect(seen.length).toBeGreaterThan(0))
}

describe('the player page’s track-action item', () => {
  beforeEach(() => {
    session.reset()
    seen.length = 0
  })
  afterEach(() => vi.unstubAllGlobals())

  it('an episode passes its series as the track-action item', async () => {
    await play(41)
    expect(seen.at(-1)).toEqual({ id: 40, title: 'Миллиарды', kind: 'series' })
  })

  it('a movie passes itself', async () => {
    await play(7)
    expect(seen.at(-1)).toEqual({ id: 7, title: 'Дюна', kind: 'movie' })
  })
})
