// A small real book library for the e2e, built from strings on every run:
// an EPUB (3 chapters of about three screens each, a fixture cover, zipped
// with the system `zip -X`, `mimetype` first and stored), an FB2 encoded in
// Windows-1251 (bytes from the table below) and a hand-written one-page PDF.
import { execFileSync } from 'node:child_process'
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'

export const BOOKS = {
  epub: 'Пробная книга',
  epubAuthor: 'Иван Тестов',
  epubSeries: 'Классика',
  fb2: 'Война и мир',
  fb2Author: 'Лев Толстой',
  pdf: 'Документ',
  /** CP1251 FB2 with no XML declaration at all. */
  fb2Undeclared: 'Анна Каренина',
  chapters: ['Глава первая. Утро', 'Глава вторая. Дорога', 'Глава третья. Город'],
  pdfText: 'Vault PDF test page',
}

// --- text ---

const SENTENCES = [
  'Утро начиналось медленно, как всегда бывает в маленьких городах, где каждый звук слышен издалека.',
  'Над рекой поднимался туман, и в нём тонули крыши домов, деревья и старая колокольня на холме.',
  'Он долго стоял у окна, глядя на пустую улицу, и думал о том, что сегодня всё должно измениться.',
  'На кухне пахло хлебом и крепким чаем; часы на стене отсчитывали минуты с привычным спокойным стуком.',
  'Дорога за околицей уходила в поле, где уже желтела рожь и кричали первые птицы.',
  'Никто не знал, сколько времени займёт путь, но все понимали, что откладывать больше нельзя.',
  'Ветер переворачивал страницы забытой на скамейке книги, словно сам хотел дочитать её до конца.',
  'Вечером зажигались фонари, и город становился другим — тише, мягче и почему-то ближе.',
  'Письмо пришло неожиданно: короткое, в несколько строк, без подписи и без обратного адреса.',
  'Она улыбнулась, сложила листок вчетверо и спрятала его в карман старого пальто.',
  'Поезд опаздывал на сорок минут, и на перроне собирались люди с чемоданами и корзинами.',
  'Каждая встреча оставляла след, даже если казалась случайной и ничего не значащей.',
]

/** Deterministic paragraphs of 4–7 sentences. */
function paragraphs(seed: number, count: number): string[] {
  let s = seed
  const next = () => ((s = (s * 1103515245 + 12345) & 0x7fffffff), s >>> 16)
  return Array.from({ length: count }, () => {
    const n = 4 + (next() % 4)
    return Array.from({ length: n }, () => SENTENCES[next() % SENTENCES.length]).join(' ')
  })
}

const esc = (t: string) => t.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

// --- EPUB ---

function chapterXhtml(title: string, i: number): string {
  const body = paragraphs(i * 7 + 1, 22)
    .map((p) => `    <p>${esc(p)}</p>`)
    .join('\n')
  return `<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="ru" xml:lang="ru">
<head><title>${esc(title)}</title></head>
<body>
  <section epub:type="chapter">
    <h2>${esc(title)}</h2>
${body}
  </section>
</body>
</html>
`
}

function writeEpub(out: string, cover: string) {
  const work = mkdtempSync(path.join(tmpdir(), 'vault-e2e-epub-'))
  try {
    const w = (rel: string, data: string) => {
      mkdirSync(path.dirname(path.join(work, rel)), { recursive: true })
      writeFileSync(path.join(work, rel), data)
    }
    w('mimetype', 'application/epub+zip')
    w(
      'META-INF/container.xml',
      `<?xml version="1.0" encoding="utf-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>
`,
    )
    const items = BOOKS.chapters.map((_, i) => `    <item id="ch${i + 1}" href="ch${i + 1}.xhtml" media-type="application/xhtml+xml"/>`).join('\n')
    const spine = BOOKS.chapters.map((_, i) => `    <itemref idref="ch${i + 1}"/>`).join('\n')
    w(
      'OEBPS/content.opf',
      `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid" xml:lang="ru">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:identifier id="uid">urn:uuid:5b0f6a3e-2c1d-4f55-9b7e-0d1e2f3a4b5c</dc:identifier>
    <dc:title>${BOOKS.epub}</dc:title>
    <dc:creator>${BOOKS.epubAuthor}</dc:creator>
    <dc:language>ru</dc:language>
    <dc:date>2020</dc:date>
    <dc:description>Небольшая книга для проверки читалки: три главы о дороге, городе и письме без подписи. Текст повторяется, но страницы перелистываются по-настоящему.</dc:description>
    <meta property="dcterms:modified">2026-01-01T00:00:00Z</meta>
    <meta name="calibre:series" content="${BOOKS.epubSeries}"/>
    <meta name="calibre:series_index" content="2"/>
    <meta name="cover" content="cover"/>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="cover" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/>
${items}
  </manifest>
  <spine>
${spine}
  </spine>
</package>
`,
    )
    w(
      'OEBPS/nav.xhtml',
      `<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="ru">
<head><title>Оглавление</title></head>
<body>
  <nav epub:type="toc"><ol>
${BOOKS.chapters.map((t, i) => `    <li><a href="ch${i + 1}.xhtml">${esc(t)}</a></li>`).join('\n')}
  </ol></nav>
</body>
</html>
`,
    )
    BOOKS.chapters.forEach((t, i) => w(`OEBPS/ch${i + 1}.xhtml`, chapterXhtml(t, i)))
    copyFileSync(cover, path.join(work, 'OEBPS', 'cover.jpg'))
    mkdirSync(path.dirname(out), { recursive: true })
    // mimetype first and stored, then the rest compressed (-X: no extra attributes)
    execFileSync('zip', ['-X', '-0', '-q', out, 'mimetype'], { cwd: work })
    execFileSync('zip', ['-X', '-9', '-q', '-r', out, 'META-INF', 'OEBPS'], { cwd: work })
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
}

// --- FB2 in Windows-1251 ---

/** Windows-1251 bytes of the characters outside ASCII that the FB2 uses. */
const CP1251: Record<string, number> = {
  Ё: 0xa8,
  ё: 0xb8,
  '«': 0xab,
  '»': 0xbb,
  '—': 0x97,
  '–': 0x96,
  '…': 0x85,
  '№': 0xb9,
  ' ': 0xa0,
}
// А..Я = 0xC0..0xDF, а..я = 0xE0..0xFF
for (let i = 0; i < 32; i++) {
  CP1251[String.fromCharCode(0x410 + i)] = 0xc0 + i
  CP1251[String.fromCharCode(0x430 + i)] = 0xe0 + i
}

export function cp1251(s: string): Buffer {
  const out = Buffer.alloc(s.length)
  for (let i = 0; i < s.length; i++) {
    const ch = s[i]!
    const code = ch.charCodeAt(0)
    if (code < 0x80) out[i] = code
    else {
      const b = CP1251[ch]
      if (b === undefined) throw new Error(`no CP1251 byte for ${JSON.stringify(ch)}`)
      out[i] = b
    }
  }
  return out
}

const TOLSTOY = [
  '— Eh bien, mon prince. Gênes et Lucques ne sont plus que des apanages, des поместья, de la famille Buonaparte.',
  'Так говорила в июле 1805 года известная Анна Павловна Шерер, фрейлина и приближённая императрицы Марии Феодоровны, встречая важного и чиновного князя Василия, первого приехавшего на её вечер.',
  'Анна Павловна кашляла несколько дней, у неё был грипп, как она говорила; грипп был тогда новое слово, употреблявшееся только редкими.',
  'В записочках, разосланных утром с красным лакеем, было написано без различия во всех: «Если у вас, граф (или князь), нет в виду ничего лучшего и если перспектива вечера у бедной больной не слишком вас пугает, то я буду очень рада видеть вас нынче у себя между семью и десятью часами».',
  '— Dieu, quelle virulente sortie! — отвечал, нисколько не смутясь такою встречей, вошедший князь, в придворном, шитом мундире, в чулках, башмаках и звёздах, с светлым выражением плоского лица.',
]

function fb2Xml(): string {
  const strip = (t: string) => t.replace(/[êç]/g, (c) => (c === 'ê' ? 'e' : 'c')) // CP1251 has no ê/ç
  const body = [...TOLSTOY.map(strip), ...paragraphs(99, 30)].map((p) => `<p>${esc(p)}</p>`).join('\n')
  return `<?xml version="1.0" encoding="windows-1251"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">
<description>
<title-info>
<genre>prose_classic</genre>
<author><first-name>Лев</first-name><last-name>Толстой</last-name></author>
<book-title>${BOOKS.fb2}</book-title>
<annotation><p>Роман-эпопея о русском обществе в эпоху войн против Наполеона.</p></annotation>
<date value="1869-01-01">1869</date>
<lang>ru</lang>
</title-info>
</description>
<body>
<title><p>${BOOKS.fb2}</p></title>
<section>
<title><p>Том первый. Часть первая</p></title>
${body}
</section>
</body>
</FictionBook>
`
}

// --- PDF ---

/** A one-page PDF with a correct xref table. */
export function onePagePdf(text: string): Buffer {
  const stream = `BT /F1 28 Tf 72 700 Td (${text}) Tj ET\nBT /F1 14 Tf 72 660 Td (A hand-written PDF for the Vault e2e.) Tj ET\n`
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>',
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
    `<< /Length ${Buffer.byteLength(stream, 'latin1')} >>\nstream\n${stream}endstream`,
  ]
  let out = '%PDF-1.4\n'
  const offsets: number[] = []
  objects.forEach((o, i) => {
    offsets.push(Buffer.byteLength(out, 'latin1'))
    out += `${i + 1} 0 obj\n${o}\nendobj\n`
  })
  const xref = Buffer.byteLength(out, 'latin1')
  out += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`
  for (const off of offsets) out += `${String(off).padStart(10, '0')} 00000 n \n`
  out += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`
  return Buffer.from(out, 'latin1')
}

/** Lays out media/books/ebooks under `root` (VAULT_MEDIA_ROOT). */
/** «Анна Каренина» in Windows-1251 without an XML declaration. */
function undeclaredFb2(): string {
  return `<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info><genre>prose_classic</genre>
<author><first-name>Лев</first-name><last-name>Толстой</last-name></author>
<book-title>${BOOKS.fb2Undeclared}</book-title><lang>ru</lang></title-info></description>
<body><section><title><p>Часть первая</p></title>
<p>Все счастливые семьи похожи друг на друга, каждая несчастливая семья несчастлива по-своему.</p>
<p>Всё смешалось в доме Облонских. Жена узнала, что муж был в связи с бывшею в их доме француженкою-гувернанткой.</p>
</section></body></FictionBook>
`
}

/** A remote image every evil section asks for: a tracking pixel. */
export const TRACKER = 'https://tracker.example/pixel.png'

/**
 * An EPUB that tries three ways to run script (external file, inline
 * <script>, <img onerror>); each would set a flag on `top` and the inline one
 * POSTs /api/me/app-password. Its first section is declared
 * «text/html; charset=utf-8», a type foliate does not parse (it arrives as a
 * raw Blob). Both sections load a remote tracking pixel. The reader must run
 * none of the scripts and fetch no remote image.
 */
export function evilEpub(): Buffer {
  const work = mkdtempSync(path.join(tmpdir(), 'vault-e2e-evil-'))
  const out = path.join(work, 'evil.epub')
  try {
    const w = (rel: string, data: string) => {
      mkdirSync(path.dirname(path.join(work, 'src', rel)), { recursive: true })
      writeFileSync(path.join(work, 'src', rel), data)
    }
    w('mimetype', 'application/epub+zip')
    w(
      'META-INF/container.xml',
      '<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>',
    )
    w(
      'OEBPS/content.opf',
      `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="uid">evil</dc:identifier><dc:title>Evil</dc:title><dc:language>en</dc:language></metadata>
<manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="c0" href="c0.html" media-type="text/html; charset=utf-8" properties="scripted"/><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml" properties="scripted"/><item id="js" href="evil.js" media-type="application/javascript"/></manifest>
<spine><itemref idref="c0"/><itemref idref="c1"/></spine></package>`,
    )
    w(
      'OEBPS/nav.xhtml',
      '<?xml version="1.0" encoding="utf-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>t</title></head><body><nav epub:type="toc"><ol><li><a href="c1.xhtml">c1</a></li></ol></nav></body></html>',
    )
    w(
      'OEBPS/c1.xhtml',
      `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>c1</title><script src="evil.js"></script><script>try{top.__inline='ran';top.fetch('/api/me/app-password',{method:'POST'}).then(function(r){top.__api=r.status})}catch(e){top.__inline='err:'+e}</script></head>
<body><p>Hello evil book</p><img src="nope.png" onerror="top.__onerror='ran'"/><img src="${TRACKER}?c1"/></body></html>`,
    )
    w(
      'OEBPS/c0.html',
      `<!DOCTYPE html><html><head><title>c0</title><script>top.__typed='ran'</script><base href="/api/"/><meta http-equiv="refresh" content="0;url=/api/me"/></head>
<body><p>Typed evil section</p><img src="x.png" onerror="top.__typedError='ran'"/><img src="${TRACKER}?c0"/><iframe srcdoc="<script>top.__srcdoc='ran'</script>"></iframe></body></html>`,
    )
    w('OEBPS/evil.js', "top.__external='ran';")
    execFileSync('zip', ['-X', '-0', '-q', out, 'mimetype'], { cwd: path.join(work, 'src') })
    execFileSync('zip', ['-X', '-9', '-q', '-r', out, 'META-INF', 'OEBPS'], { cwd: path.join(work, 'src') })
    return readFileSync(out)
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
}

export function seedBooks(root: string, images: string) {
  const ebooks = path.join(root, 'media', 'books', 'ebooks')
  writeEpub(path.join(ebooks, BOOKS.epubAuthor, `${BOOKS.epub}.epub`), path.join(images, 'garlic-poster.jpg'))
  const fb2 = path.join(ebooks, BOOKS.fb2Author, `${BOOKS.fb2}.fb2`)
  mkdirSync(path.dirname(fb2), { recursive: true })
  writeFileSync(fb2, cp1251(fb2Xml()))
  writeFileSync(path.join(ebooks, `${BOOKS.pdf}.pdf`), onePagePdf(BOOKS.pdfText))
  writeFileSync(path.join(ebooks, BOOKS.fb2Author, `${BOOKS.fb2Undeclared}.fb2`), cp1251(undeclaredFb2()))
}
