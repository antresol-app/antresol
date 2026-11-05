import { describe, expect, it, vi } from 'vitest'
import { audioAccept } from '../caps'

describe('audio caps', () => {
  it('audioAccept maps canPlayType answers', () => {
    vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockImplementation((t: string) =>
      t === 'audio/flac' ? 'probably' : t === 'audio/mpeg' ? 'maybe' : '',
    )
    expect(audioAccept()).toEqual(['flac', 'mp3'])
  })

  it('asks for every codec the server can send', () => {
    const asked: string[] = []
    vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockImplementation((t: string) => {
      asked.push(t)
      return 'maybe'
    })
    expect(audioAccept()).toEqual(['flac', 'mp3', 'aac', 'alac', 'opus', 'vorbis'])
    expect(asked).toContain('audio/mp4; codecs="alac"')
  })
})
