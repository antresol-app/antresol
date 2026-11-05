// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2026 Antresol Contributors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Book content is untrusted (books come from anywhere). The app's
// CSP already blocks scripts in foliate's same-origin section iframes; this is
// the second wall: script resources are refused and (X)HTML/SVG sections lose
// <script>, on* handlers and javascript: URLs before they become blob: URLs.
const MARKUP = /^(application\/xhtml\+xml|text\/html|image\/svg\+xml)/i
const XHTML_NS = 'http://www.w3.org/1999/xhtml'

/**
 * The policy every section document gets on top of the app's: no scripts,
 * no plugins, no frames, and no remote images or fonts (a tracking pixel
 * would tell a stranger that the book was opened, and from where).
 */
export const FRAME_CSP =
  "default-src 'none'; script-src 'none'; object-src 'none'; frame-src 'none'; img-src 'self' blob: data:; " +
  "media-src 'self' blob: data:; style-src 'self' 'unsafe-inline' blob: data:; font-src 'self' blob: data:; " +
  "base-uri 'none'; form-action 'none'"

/** «text/html; charset=utf-8» → «text/html». */
export const baseType = (type: string) => (type || '').split(';')[0]!.trim().toLowerCase()

/** Markup types sanitized here, by the exact type the section is then given. */
function markupKind(base: string): 'text/html' | 'application/xhtml+xml' | 'image/svg+xml' | null {
  if (base === 'text/html') return 'text/html'
  if (base === 'image/svg+xml') return 'image/svg+xml'
  if (base === 'application/xhtml+xml' || base === 'application/xml' || base === 'text/xml' || base.endsWith('+xml')) return 'application/xhtml+xml'
  return null
}

/** Book resources that may load at all: media, fonts, CSS, plain text and markup this file sanitizes. */
export function allowedType(type: string): boolean {
  const b = baseType(type)
  if (b === '') return true // broken manifests: handled (sniffed) in the data hook
  if (/^(image|audio|video|font)\//.test(b)) return true
  if (b === 'text/css' || b === 'text/plain') return true
  if (/^application\/(x-)?font-|^application\/vnd\.ms-opentype$/.test(b)) return true
  return markupKind(b) !== null
}
const URL_ATTRS = ['href', 'src', 'xlink:href', 'action', 'formaction', 'data', 'poster']

function addFramePolicy(doc: Document) {
  const root = doc.documentElement
  if (!root || root.localName !== 'html') return
  const ns = root.namespaceURI || XHTML_NS
  let head = Array.from(root.children).find((c) => c.localName === 'head')
  if (!head) {
    head = doc.createElementNS(ns, 'head')
    root.insertBefore(head, root.firstChild)
  }
  const meta = doc.createElementNS(ns, 'meta')
  meta.setAttribute('http-equiv', 'Content-Security-Policy')
  meta.setAttribute('content', FRAME_CSP)
  head.insertBefore(meta, head.firstChild)
}

function cleanDoc(doc: Document) {
  for (const el of Array.from(doc.querySelectorAll('script, iframe, object, embed, frame, frameset, base'))) el.remove()
  for (const el of Array.from(doc.querySelectorAll('meta[http-equiv]'))) el.remove()
  for (const el of Array.from(doc.getElementsByTagName('*'))) {
    for (const a of Array.from(el.attributes)) {
      const name = a.name.toLowerCase()
      if (name.startsWith('on')) el.removeAttribute(a.name)
      else if (URL_ATTRS.includes(name) && /^\s*(javascript|vbscript|data:text\/html)/i.test(a.value)) el.removeAttribute(a.name)
    }
  }
}

/** Last resort for markup the parser rejects. */
function scrub(text: string): string {
  const out = text
    .replace(/<script\b[\s\S]*?<\/script\s*>/gi, '')
    .replace(/<script\b[^>]*\/?>/gi, '')
    .replace(/<(iframe|object|embed|frame|frameset|base)\b[\s\S]*?(<\/\1\s*>|\/>)/gi, '')
    .replace(/\son[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)/gi, '')
    .replace(/(href|src)\s*=\s*(["'])\s*javascript:[^"']*\2/gi, '$1="#"')
    .replace(/<meta\b[^>]*http-equiv[^>]*>/gi, '')
  const meta = `<meta http-equiv="Content-Security-Policy" content="${FRAME_CSP}"/>`
  return /<head\b[^>]*>/i.test(out) ? out.replace(/<head\b[^>]*>/i, (h) => h + meta) : out.replace(/<html\b[^>]*>/i, (h) => `${h}<head>${meta}</head>`)
}

/** Returns the markup without scripts; other types pass through. */
export function sanitizeMarkup(text: string, type: string): string {
  const kind = markupKind(baseType(type))
  if (!kind || !MARKUP.test(kind)) return text
  const mime = kind
  try {
    const doc = new DOMParser().parseFromString(text, mime)
    if (mime !== 'text/html' && doc.getElementsByTagName('parsererror').length) return scrub(text)
    cleanDoc(doc)
    addFramePolicy(doc)
    if (mime === 'text/html') {
      const dt = doc.doctype ? '<!DOCTYPE html>\n' : ''
      return dt + doc.documentElement.outerHTML
    }
    const out = new XMLSerializer().serializeToString(doc)
    // keep the declaration (encoding) the section had
    const decl = /^\s*<\?xml[^>]*\?>/.exec(text)?.[0]
    return decl && !out.startsWith('<?xml') ? `${decl}\n${out}` : out
  } catch {
    return scrub(text)
  }
}

interface LoadDetail {
  type?: string
  isScript?: boolean
  allow: boolean | Promise<boolean>
}
interface DataDetail {
  type: string | Promise<string>
  data: unknown
}

/** Wires both walls into a foliate book's transformTarget (EPUB, MOBI/AZW3). */
export function guardBook(book: { transformTarget?: EventTarget }) {
  const t = book.transformTarget
  if (!t) return
  t.addEventListener('load', (e) => {
    const d = (e as CustomEvent<LoadDetail>).detail
    if (d.isScript || !allowedType(d.type ?? '')) d.allow = false
  })
  t.addEventListener('data', (e) => {
    const d = (e as CustomEvent<DataDetail>).detail
    const prev = d.data
    const result = Promise.all([Promise.resolve(prev), Promise.resolve(d.type)]).then(async ([data, type]) => {
      const base = baseType(type)
      let kind = markupKind(base)
      // foliate parses only exact markup types; anything else arrives as a Blob
      const blob = typeof Blob !== 'undefined' && data instanceof Blob ? data : null
      if (blob && !kind && base === '') {
        const head = (await blob.slice(0, 512).text()).replace(/^\uFEFF/, '').trimStart()
        if (head.startsWith('<') && !/^<svg\b/i.test(head)) kind = 'text/html'
      }
      if (!kind) return { data, type }
      const text = typeof data === 'string' ? data : blob ? await blob.text() : null
      if (text === null) return { data, type }
      return { data: sanitizeMarkup(text, kind), type: kind }
    })
    d.data = result.then((r) => r.data)
    d.type = result.then((r) => r.type)
  })
}
