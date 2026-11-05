import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { audio, resetAudioForTests } from '../../../player/audio/engine'
import { Songs } from '../Songs'
import { json, song } from './fixtures'

let urls: string[]

beforeEach(() => {
  resetAudioForTests()
  urls = []
  vi.stubGlobal('IntersectionObserver', class {
    observe() {}
    disconnect() {}
  })
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      urls.push(url)
      if (url.startsWith('/api/music/songs')) return json({ items: [song(1), song(2)], total: 2 })
      if (url.startsWith('/api/music/shuffle')) return json({ tracks: [song(5), song(6), song(7)] })
      return json({}, 404)
    }),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  resetAudioForTests()
})

describe('Songs', () => {
  it('«Перемешать все» shuffles through the server', async () => {
    render(
      <MemoryRouter>
        <Songs />
      </MemoryRouter>,
    )
    fireEvent.click(await screen.findByRole('button', { name: 'Перемешать все' }))
    await waitFor(() => expect(audio.get().queue).toHaveLength(3))
    expect(urls).toContain('/api/music/shuffle?source=all')
    expect(urls.some((u) => u.startsWith('/api/music/songs') && u.includes('limit=500'))).toBe(false)
  })
})
