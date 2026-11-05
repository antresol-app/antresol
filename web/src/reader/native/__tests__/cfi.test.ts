// cfiMatchesWeb: the CFI the web reader saves for the start of tiny.epub,
// computed with the web's own code path (bookFile → makeBook → guardBook,
// as in Reader/EpubReader) and foliate's CFI module, kept in
// fixtures/tiny.epub.cfi (UPDATE_CFI=1 rewrites it).
//
// jsdom has no layout: foliate's paginator never loads its section iframe
// here, so `view.init()` emits no relocate at all (checked while writing
// this test). The page range is therefore built the way the paginator's
// getVisibleRange builds it when the whole section fits on the first page
// (the first visible node to the end of the last text node), which is the
// case for both tiny chapters. The real rendered CFI is compared with this
// file by the app's FoliateIntegrationTests.foliateOpensFixtureEpub (the
// native bundle in a WKWebView), so the app and the web agree on it.
import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { expect, test } from 'vitest'
import * as CFI from '../../../vendor/foliate/epubcfi.js'
import { makeBook } from '../../../vendor/foliate/view.js'
import { bookFile } from '../../bookFile'
import { guardBook } from '../../sanitize'

const here = dirname(fileURLToPath(import.meta.url))
const EPUB = join(here, 'fixtures/tiny.epub')
const CFI_FILE = join(here, 'fixtures/tiny.epub.cfi')

interface Section {
  cfi?: string
  createDocument(): Promise<Document>
}

/** The paginator's visible range of a page that shows the whole section. */
function wholePageRange(doc: Document): Range {
  const walker = doc.createTreeWalker(doc.body, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT | NodeFilter.SHOW_CDATA_SECTION, {
    acceptNode: (node) => {
      const name = (node as Element).localName?.toLowerCase()
      if (name === 'script' || name === 'style') return NodeFilter.FILTER_REJECT
      if (node.nodeType === 1) return NodeFilter.FILTER_ACCEPT
      return node.nodeValue?.trim() ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_SKIP
    },
  })
  const nodes: Node[] = []
  for (let n = walker.nextNode(); n; n = walker.nextNode()) nodes.push(n)
  const from = nodes[0] ?? doc.body
  const to = nodes[nodes.length - 1] ?? from
  const range = doc.createRange()
  range.setStart(from, 0)
  range.setEnd(to, to.nodeType === 1 ? 0 : (to.nodeValue?.length ?? 0))
  return range
}

test('cfiMatchesWeb', async () => {
  const file = await bookFile('EPUB', new Blob([readFileSync(EPUB)]))
  const book = await makeBook(file)
  guardBook(book)
  const section = book.sections[0] as unknown as Section
  const doc = await section.createDocument()
  // view.getCFI(index, range)
  const cfi = CFI.joinIndir(section.cfi ?? CFI.fake.fromIndex(0), CFI.fromRange(wholePageRange(doc)))
  expect(cfi.startsWith('epubcfi(/6/2!')).toBe(true)
  // it resolves (as the web reader resolves a saved position) to the first chapter's heading
  const resolved = (book as unknown as { resolveCFI(c: string): { index: number; anchor(d: Document): Range } }).resolveCFI(cfi)
  expect(resolved.index).toBe(0)
  expect(resolved.anchor(doc).toString()).toMatch(/^Глава первая/)

  if (process.env.UPDATE_CFI === '1') writeFileSync(CFI_FILE, `${cfi}\n`)
  expect(cfi).toBe(readFileSync(CFI_FILE, 'utf8').trim())
})
