// «Жанры» on the music home: one scrolling row of genre chips; a chip
// shuffles that genre. Hidden when the library has no genres.
import { useState } from 'react'
import type { Genre } from '../../api/types'
import { Button } from '../../ui/Button'
import { playShuffled } from './shuffle'
import styles from './GenreRow.module.css'
import { useT } from '../../i18n'

export function GenreRow({ genres, onError }: { genres: Genre[]; onError?: (message: string) => void }) {
  const t = useT()
  const [busy, setBusy] = useState('')
  if (genres.length === 0) return null

  const shuffle = (name: string) => {
    setBusy(name)
    onError?.('')
    void playShuffled({ kind: 'genre', genre: name }).then((err) => {
      setBusy('')
      if (err) onError?.(err)
    })
  }

  return (
    <section className={styles.section} aria-labelledby="music-genres">
      <h2 id="music-genres" className={styles.title}>
        {t('music.genres')}
      </h2>
      <ul className={styles.row}>
        {genres.map((g) => (
          <li key={g.name} className={styles.cell}>
            <Button variant="secondary" size="small" loading={busy === g.name} aria-label={t('music.shuffleGenre', { name: g.name })} onClick={() => shuffle(g.name)}>
              {g.name}
            </Button>
          </li>
        ))}
      </ul>
    </section>
  )
}
