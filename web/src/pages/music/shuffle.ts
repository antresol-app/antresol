// «Перемешать»: the server draws up to 500 random tracks of a source (the
// whole library, a genre, an artist's catalogue) and they play shuffled.
import { ApiError, getShuffle } from '../../api/client'
import type { ShuffleSource } from '../../api/types'
import { audio } from '../../player/audio/engine'
import { songItems } from '../../player/audio/queue'
import { t } from '../../i18n'

export { shuffleQuery } from '../../api/client'

const shuffleFailed = () => t('music.shuffleFailed')

/** Plays the source shuffled; returns null, or the text to show when it failed (the queue is then untouched). */
export async function playShuffled(source: ShuffleSource): Promise<string | null> {
  try {
    const tracks = await getShuffle(source)
    if (tracks.length === 0) return shuffleFailed()
    audio.play(songItems(tracks), 0, { shuffle: true })
    return null
  } catch (e) {
    return e instanceof ApiError && e.status !== 0 ? e.message : shuffleFailed()
  }
}
