// Media root for the «Файлы» e2e: a movie folder with a small real video
// (ffmpeg test pattern, cached in the OS temp dir), its poster, subtitles and
// .nfo; a series; music; books (a tiny PDF and a text file) and downloads.
import { execFileSync } from 'node:child_process'
import { copyFileSync, existsSync, linkSync, mkdirSync, renameSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'

const CACHE = path.join(tmpdir(), 'vault-e2e-files-v1')

export const FILES = {
  movie: { folder: 'Тестовый фильм (2024)', title: 'Тестовый фильм', video: 'Тестовый фильм (2024).mp4', srt: 'Тестовый фильм (2024).ru.srt' },
  other: { folder: 'Брат 2 (2000)', video: 'Брат 2 (2000) BDRip 1080p.mp4' },
  series: { folder: 'Планета Земля II (2016)' },
  renamed: 'Тестовый фильм — режиссёрская версия (2024)',
}

const SRT = `1
00:00:00,500 --> 00:00:05,000
Привет из субтитров
`

function ffmpeg(out: string, args: string[]) {
  if (existsSync(out)) return
  mkdirSync(CACHE, { recursive: true })
  const tmp = `${out}.${process.pid}.tmp${path.extname(out)}`
  execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', ...args, tmp], { stdio: 'inherit' })
  renameSync(tmp, out)
}

function samples() {
  const video = path.join(CACHE, 'h264-aac-8.mp4')
  ffmpeg(video, [
    '-f', 'lavfi', '-i', 'testsrc2=size=640x360:rate=24:duration=8',
    '-f', 'lavfi', '-i', 'sine=frequency=440:sample_rate=48000:duration=8',
    '-c:v', 'libx264', '-preset', 'veryfast', '-pix_fmt', 'yuv420p', '-c:a', 'aac', '-b:a', '96k', '-movflags', '+faststart', '-shortest',
  ])
  const audio = path.join(CACHE, 'tone-6.m4a')
  ffmpeg(audio, ['-f', 'lavfi', '-i', 'sine=frequency=523:sample_rate=44100:duration=6', '-c:a', 'aac', '-b:a', '96k'])
  return { video, audio }
}

function place(from: string, to: string) {
  mkdirSync(path.dirname(to), { recursive: true })
  try {
    linkSync(from, to)
  } catch {
    copyFileSync(from, to)
  }
}

/** A one-page PDF with a line of text (hand-written, valid xref offsets). */
function pdf(): Buffer {
  const objs = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 420 595] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>',
    '', // stream, filled below
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ]
  const text = 'BT /F1 22 Tf 60 500 Td (Vault test book) Tj ET'
  objs[3] = `<< /Length ${text.length} >>\nstream\n${text}\nendstream`
  let out = '%PDF-1.4\n'
  const offs: number[] = []
  objs.forEach((o, i) => {
    offs.push(out.length)
    out += `${i + 1} 0 obj\n${o}\nendobj\n`
  })
  const xref = out.length
  out += `xref\n0 ${objs.length + 1}\n0000000000 65535 f \n${offs.map((o) => `${String(o).padStart(10, '0')} 00000 n \n`).join('')}`
  out += `trailer\n<< /Size ${objs.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`
  return Buffer.from(out, 'latin1')
}

/** Lays out the «Файлы» library under `root` (VAULT_MEDIA_ROOT). */
export function seedFiles(root: string, images: string) {
  const s = samples()
  const movies = path.join(root, 'media', 'movies')
  const m = FILES.movie
  const mDir = path.join(movies, m.folder)
  place(s.video, path.join(mDir, m.video))
  writeFileSync(path.join(mDir, m.srt), SRT)
  writeFileSync(path.join(mDir, `${m.folder}.nfo`), `<movie><title>${m.title}</title><year>2024</year><plot>Короткий тестовый ролик.</plot></movie>`)
  copyFileSync(path.join(images, 'interstellar-poster.jpg'), path.join(mDir, 'poster.jpg'))
  copyFileSync(path.join(images, 'interstellar-backdrop.jpg'), path.join(mDir, 'fanart.jpg'))

  const o = FILES.other
  const oDir = path.join(movies, o.folder)
  place(s.video, path.join(oDir, o.video))
  copyFileSync(path.join(images, 'wolf-poster.jpg'), path.join(oDir, 'poster.jpg'))

  const sDir = path.join(root, 'media', 'series', FILES.series.folder, 'Season 01')
  ;['Острова', 'Горы', 'Джунгли'].forEach((_, i) => place(s.video, path.join(sDir, `Планета Земля II.S01E0${i + 1}.mp4`)))
  copyFileSync(path.join(images, 'pe2-poster.jpg'), path.join(root, 'media', 'series', FILES.series.folder, 'poster.jpg'))

  place(s.audio, path.join(root, 'media', 'music', 'Настроечные тоны', '01 — До.m4a'))
  mkdirSync(path.join(root, 'media', 'books'), { recursive: true })
  writeFileSync(path.join(root, 'media', 'books', 'Тестовая книга.pdf'), pdf())
  writeFileSync(path.join(root, 'media', 'books', 'Список чтения.txt'), 'Мастер и Маргарита\nДвенадцать стульев\nПикник на обочине\n')

  const dl = path.join(root, 'downloads', 'Seven.Worlds.One.Planet.S01.1080p.BluRay')
  place(s.video, path.join(dl, 'Seven.Worlds.One.Planet.S01E01.1080p.mkv'))
  writeFileSync(path.join(dl, 'Seven.Worlds.One.Planet.nfo'), 'Release notes\n')
  mkdirSync(path.join(root, 'tmp', 'vault-stream'), { recursive: true })
}
