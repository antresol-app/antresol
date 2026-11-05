// Small real videos for the playback e2e, generated with ffmpeg (lavfi test
// patterns) on first use and cached in the OS temp dir — nothing big is
// committed. Laid out the way the scanner expects, with local artwork.
import { execFileSync } from 'node:child_process'
import { copyFileSync, existsSync, linkSync, mkdirSync, renameSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'

const CACHE = path.join(tmpdir(), 'vault-e2e-media-v1')

export const PLAYBACK = {
  /** H.264 + AAC in MP4 with a Russian .srt next to it: plays directly. */
  direct: { title: 'Тестовый фильм', year: 2024, seconds: 40 },
  /** HEVC + AC-3 in MKV: never direct in a browser, goes through HLS. */
  hls: { title: 'Волк с Уолл-стрит', year: 2013, seconds: 40 },
  /** Two parts (cd1, cd2) played in a row. */
  parts: { title: 'Фильм в двух частях', year: 2001, seconds: 14 },
  /** Three short episodes for «Следующая серия». */
  series: { title: 'Планета Земля II', year: 2016, seconds: 14, episodes: ['Острова', 'Горы', 'Джунгли'] },
}

const SRT = `1
00:00:00,500 --> 00:00:30,000
Привет из субтитров

2
00:00:30,500 --> 00:00:39,000
Вторая реплика
`

function ffmpeg(out: string, args: string[]) {
  if (existsSync(out)) return
  mkdirSync(CACHE, { recursive: true })
  const tmp = `${out}.${process.pid}.tmp${path.extname(out)}`
  execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', ...args, tmp], { stdio: 'inherit' })
  renameSync(tmp, out) // atomic: parallel workers never see a half-written file
}

function samples() {
  const src = (sec: number, rate = 24) => [
    '-f', 'lavfi', '-i', `testsrc2=size=640x360:rate=${rate}:duration=${sec}`,
    '-f', 'lavfi', '-i', `sine=frequency=440:sample_rate=48000:duration=${sec}`,
  ]
  const h264 = path.join(CACHE, 'h264-aac-40.mp4')
  ffmpeg(h264, [...src(40), '-c:v', 'libx264', '-preset', 'veryfast', '-pix_fmt', 'yuv420p', '-g', '48',
    '-c:a', 'aac', '-b:a', '96k', '-movflags', '+faststart', '-shortest'])
  const hevc = path.join(CACHE, 'hevc-ac3-40.mkv')
  ffmpeg(hevc, [...src(40), '-c:v', 'libx265', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p', '-tag:v', 'hvc1',
    '-x265-params', 'log-level=error', '-c:a', 'ac3', '-b:a', '192k', '-shortest'])
  const short = path.join(CACHE, 'h264-aac-14.mp4')
  ffmpeg(short, [...src(14), '-c:v', 'libx264', '-preset', 'veryfast', '-pix_fmt', 'yuv420p', '-g', '48',
    '-c:a', 'aac', '-b:a', '96k', '-movflags', '+faststart', '-shortest'])
  return { h264, hevc, short }
}

function place(from: string, to: string) {
  mkdirSync(path.dirname(to), { recursive: true })
  try {
    linkSync(from, to)
  } catch {
    copyFileSync(from, to)
  }
}

function art(images: string, key: string, dir: string, logo = false) {
  copyFileSync(path.join(images, `${key}-poster.jpg`), path.join(dir, 'poster.jpg'))
  copyFileSync(path.join(images, `${key}-backdrop.jpg`), path.join(dir, 'fanart.jpg'))
  if (logo && existsSync(path.join(images, `${key}-logo.png`))) copyFileSync(path.join(images, `${key}-logo.png`), path.join(dir, 'clearlogo.png'))
}

/** Lays out the playback library under `root` (VAULT_MEDIA_ROOT). */
export function seedPlayback(root: string, images: string) {
  const s = samples()
  const movies = path.join(root, 'media', 'movies')
  const series = path.join(root, 'media', 'series')
  mkdirSync(movies, { recursive: true })
  mkdirSync(series, { recursive: true })

  const d = PLAYBACK.direct
  const dDir = path.join(movies, `${d.title} (${d.year})`)
  place(s.h264, path.join(dDir, `${d.title} (${d.year}).mp4`))
  writeFileSync(path.join(dDir, `${d.title} (${d.year}).ru.srt`), SRT)
  writeFileSync(
    path.join(dDir, `${d.title} (${d.year}).nfo`),
    `<movie><title>${d.title}</title><year>${d.year}</year><mpaa>12+</mpaa><plot>Короткий тестовый ролик: настроечная таблица и тон 440 Гц. Проверяет прямое воспроизведение, субтитры и сохранение прогресса.</plot></movie>`,
  )
  art(images, 'interstellar', dDir)

  const h = PLAYBACK.hls
  const hDir = path.join(movies, `${h.title} (${h.year})`)
  place(s.hevc, path.join(hDir, `${h.title} (${h.year}).mkv`))
  art(images, 'wolf', hDir)

  const p = PLAYBACK.parts
  const pDir = path.join(movies, `${p.title} (${p.year})`)
  place(s.short, path.join(pDir, `${p.title} (${p.year}) - cd1.mp4`))
  place(s.short, path.join(pDir, `${p.title} (${p.year}) - cd2.mp4`))
  art(images, 'catchme', pDir)

  const sr = PLAYBACK.series
  const sDir = path.join(series, `${sr.title} (${sr.year})`)
  sr.episodes.forEach((name, i) => {
    const n = String(i + 1).padStart(2, '0')
    const base = path.join(sDir, 'Season 01', `${sr.title}.S01E${n}`)
    place(s.short, `${base}.mp4`)
    writeFileSync(`${base}.nfo`, `<episodedetails><title>${name}</title><season>1</season><episode>${i + 1}</episode><plot>Серия ${i + 1}: ${name}.</plot></episodedetails>`)
  })
  art(images, 'pe2', sDir, true)
}
