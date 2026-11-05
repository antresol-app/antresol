// A small real music library for the e2e: 20-second FLAC sine tones with
// tags and an embedded square cover, generated with ffmpeg on first use and
// cached in the OS temp dir (nothing big is committed), hardlinked into
// media/music the way the scanner expects.
import { execFileSync } from 'node:child_process'
import { copyFileSync, existsSync, linkSync, mkdirSync, renameSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'

const CACHE = path.join(tmpdir(), 'vault-e2e-media-v1', 'music')

export const MUSIC = {
  album: 'Первый альбом',
  artist: 'Тестовый артист',
  tracks: ['Трек 1', 'Трек 2', 'Трек 3'],
  compilation: 'Сборник хитов',
  /** Tracks of the compilation, each by its own artist. */
  hits: [
    { title: 'Очень длинное название песни, которое не помещается в одну строку', artist: 'Исполнитель с длинным именем' },
    { title: 'Вторая песня', artist: 'Другой исполнитель' },
  ],
  seconds: 20,
  /** Synced lyrics of the first album track (a «.lrc» beside it). */
  lyrics: [
    [0, 'Первая строка песни'],
    [4, 'Вторая строка песни'],
    [8, 'Третья строка песни'],
    [12, 'Последняя строка'],
  ] as [number, string][],
}

function ffmpeg(out: string, args: string[]) {
  if (existsSync(out)) return
  mkdirSync(path.dirname(out), { recursive: true })
  const tmp = `${out}.${process.pid}.tmp${path.extname(out)}`
  execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', ...args, tmp], { stdio: 'inherit' })
  renameSync(tmp, out) // atomic: parallel workers never see a half-written file
}

function place(from: string, to: string) {
  mkdirSync(path.dirname(to), { recursive: true })
  try {
    linkSync(from, to)
  } catch {
    copyFileSync(from, to)
  }
}

/** The square cover: tiny-poster.jpg cropped to its centre, 600×600. */
function cover(images: string) {
  const out = path.join(CACHE, 'cover-600.jpg')
  ffmpeg(out, ['-i', path.join(images, 'tiny-poster.jpg'), '-vf', "crop='min(iw,ih)':'min(iw,ih)',scale=600:600", '-q:v', '3', '-f', 'image2'])
  return out
}

function track(name: string, freq: number, art: string, tags: Record<string, string>) {
  const out = path.join(CACHE, `${name}.flac`)
  const meta = Object.entries(tags).flatMap(([k, v]) => ['-metadata', `${k}=${v}`])
  ffmpeg(out, [
    '-f', 'lavfi', '-i', `sine=frequency=${freq}:sample_rate=44100:duration=${MUSIC.seconds}`,
    '-i', art,
    '-map', '0:a', '-map', '1:v', '-c:v', 'copy', '-disposition:v', 'attached_pic', '-metadata:s:v', 'comment=Cover (front)',
    '-c:a', 'flac', ...meta, '-f', 'flac',
  ])
  return out
}

/** Lays out media/music under `root` (VAULT_MEDIA_ROOT). */
export function seedMusic(root: string, images: string) {
  const art = cover(images)
  const music = path.join(root, 'media', 'music')
  const albumDir = path.join(music, MUSIC.artist, MUSIC.album)
  MUSIC.tracks.forEach((title, i) => {
    const f = track(`album-${i + 1}`, 330 + i * 110, art, {
      title,
      artist: MUSIC.artist,
      album_artist: MUSIC.artist,
      album: MUSIC.album,
      track: String(i + 1),
      disc: '1',
      date: '1988',
      genre: 'Рок',
    })
    const name = `${String(i + 1).padStart(2, '0')} - ${title}`
    place(f, path.join(albumDir, `${name}.flac`))
    if (i === 0) {
      const lrc = MUSIC.lyrics.map(([t, line]) => `[00:${String(t).padStart(2, '0')}.00]${line}`).join('\n')
      writeFileSync(path.join(albumDir, `${name}.lrc`), `[ar:${MUSIC.artist}]\n${lrc}\n`)
    }
  })
  const compDir = path.join(music, 'Various Artists', MUSIC.compilation)
  MUSIC.hits.forEach((h, i) => {
    const f = track(`hits-${i + 1}`, 520 + i * 90, art, {
      title: h.title,
      artist: h.artist,
      album_artist: 'Various Artists',
      album: MUSIC.compilation,
      compilation: '1',
      track: String(i + 1),
      date: '2020',
      genre: 'Поп',
    })
    place(f, path.join(compDir, `${String(i + 1).padStart(2, '0')} - ${i + 1}.flac`))
  })
}
