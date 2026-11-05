// Shared test data for the music page tests.
import type { Song } from '../../../api/types'

export function song(i: number, over: Partial<Song> = {}): Song {
  return {
    id: 100 + i,
    title: `Трек ${i}`,
    artist: 'Тестовый артист',
    artistId: 5,
    albumId: 1,
    album: 'Первый альбом',
    discNo: 1,
    trackNo: i,
    durationMs: 200_000,
    fileId: 200 + i,
    lossless: true,
    hasLyrics: false,
    cover: '/api/images/local/1/poster',
    starred: false,
    ...over,
  }
}

export const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status })
