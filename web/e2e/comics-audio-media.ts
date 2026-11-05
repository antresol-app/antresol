// Manga and an audiobook for the «Книги» e2e (library 'books'): two CBZ
// volumes of fixture posters with a ComicInfo.xml that says right to left,
// and a three-file MP3 audiobook (20 s sine tones, generated with ffmpeg on
// first use and cached in the OS temp dir, hardlinked in) with a square
// cover and a reader.txt.
import { execFileSync } from 'node:child_process'
import { copyFileSync, existsSync, linkSync, mkdirSync, mkdtempSync, renameSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'

const CACHE = path.join(tmpdir(), 'vault-e2e-media-v1', 'audiobooks')

export const COMICS_AUDIO = {
  comic: 'Тестовая манга',
  comicAuthor: 'Тестовый мангака',
  /** Pages of «Том 1» and «Том 2». */
  pages: [6, 3],
  audiobook: 'Тестовая аудиокнига',
  author: 'Тестовый автор',
  narrator: 'Иван Чтецов',
  seconds: 20,
  files: 3,
}

const POSTERS = ['garlic', 'africa', 'bp2', 'catchme', 'drain', 'elections', 'interstellar', 'pe1', 'pe2']

function comicInfo(volume: number, pages: number) {
  return `<?xml version="1.0" encoding="utf-8"?>
<ComicInfo xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <Series>${COMICS_AUDIO.comic}</Series>
  <Volume>${volume}</Volume>
  <Writer>${COMICS_AUDIO.comicAuthor}</Writer>
  <Year>2019</Year>
  <PageCount>${pages}</PageCount>
  <LanguageISO>ru</LanguageISO>
  <Manga>YesAndRightToLeft</Manga>
</ComicInfo>
`
}

/** A file name in POSTERS is a fixture poster; an absolute path is used as is. */
function writeCbz(out: string, images: string, posters: string[], volume: number) {
  const work = mkdtempSync(path.join(tmpdir(), 'vault-e2e-cbz-'))
  try {
    posters.forEach((p, i) =>
      copyFileSync(path.isAbsolute(p) ? p : path.join(images, `${p}-poster.jpg`), path.join(work, `${String(i + 1).padStart(3, '0')}.jpg`)),
    )
    writeFileSync(path.join(work, 'ComicInfo.xml'), comicInfo(volume, posters.length))
    mkdirSync(path.dirname(out), { recursive: true })
    execFileSync('zip', ['-X', '-0', '-q', '-j', out, ...posters.map((_, i) => `${String(i + 1).padStart(3, '0')}.jpg`), 'ComicInfo.xml'], { cwd: work })
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
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

/** Lays out media/books/manga and media/books/audiobooks under `root`. */
export function seedComicsAudio(root: string, images: string) {
  const manga = path.join(root, 'media', 'books', 'manga', COMICS_AUDIO.comic)
  writeCbz(path.join(manga, `${COMICS_AUDIO.comic} v01.cbz`), images, POSTERS.slice(0, COMICS_AUDIO.pages[0]), 1)
  // «Том 2» ends with a tall webtoon-like strip (three posters stacked)
  const strip = path.join(CACHE, 'strip-375x1653.jpg')
  ffmpeg(strip, [
    ...['pe1', 'pe2', 'interstellar'].flatMap((p) => ['-i', path.join(images, `${p}-poster.jpg`)]),
    '-filter_complex', '[0:v]scale=375:551[a];[1:v]scale=375:551[b];[2:v]scale=375:551[c];[a][b][c]vstack=inputs=3',
    '-q:v', '4', '-f', 'image2',
  ])
  writeCbz(path.join(manga, `${COMICS_AUDIO.comic} v02.cbz`), images, ['interstellar', 'pe1', strip], 2)

  const book = path.join(root, 'media', 'books', 'audiobooks', COMICS_AUDIO.author, COMICS_AUDIO.audiobook)
  const cover = path.join(CACHE, 'cover-600.jpg')
  ffmpeg(cover, ['-i', path.join(images, 'sevenworlds-poster.jpg'), '-vf', "crop='min(iw,ih)':'min(iw,ih)',scale=600:600", '-q:v', '3', '-f', 'image2'])
  for (let i = 1; i <= COMICS_AUDIO.files; i++) {
    const f = path.join(CACHE, `part-${i}.mp3`)
    ffmpeg(f, ['-f', 'lavfi', '-i', `sine=frequency=${220 + i * 110}:sample_rate=44100:duration=${COMICS_AUDIO.seconds}`, '-c:a', 'libmp3lame', '-b:a', '64k', '-f', 'mp3'])
    place(f, path.join(book, `${String(i).padStart(2, '0')}.mp3`))
  }
  copyFileSync(cover, path.join(book, 'cover.jpg'))
  writeFileSync(path.join(book, 'reader.txt'), `${COMICS_AUDIO.narrator}\n`)
}
