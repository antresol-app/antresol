// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2026 Antresol Contributors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// FB2 files from Russian libraries are often Windows-1251 without (or with a
// wrong) XML encoding declaration. foliate decodes those as UTF-8, so the
// reader normalizes them first — the same fallback the server scanner uses.
const DECL = /^\s*<\?xml[^>]*\?>/
const UTF8_DECL = '<?xml version="1.0" encoding="utf-8"?>'

/** The FB2 as UTF-8 when it is not valid UTF-8 and declares no other encoding; else unchanged. */
export async function fb2ToUtf8(file: Blob): Promise<Blob> {
  const buf = new Uint8Array(await file.arrayBuffer())
  if (buf[0] === 0xef && buf[1] === 0xbb && buf[2] === 0xbf) return file // UTF-8 BOM
  try {
    new TextDecoder('utf-8', { fatal: true }).decode(buf)
    return file
  } catch {
    /* not UTF-8 */
  }
  const head = new TextDecoder('latin1').decode(buf.subarray(0, 200))
  const enc = /^\s*<\?xml[^>]*encoding\s*=\s*["']([A-Za-z0-9._-]+)["']/.exec(head)?.[1]?.toLowerCase()
  if (enc && enc !== 'utf-8' && enc !== 'utf8') return file // foliate decodes declared encodings itself
  let text = new TextDecoder('windows-1251').decode(buf)
  text = DECL.test(text) ? text.replace(DECL, UTF8_DECL) : `${UTF8_DECL}\n${text}`
  return new Blob([text], { type: 'application/x-fictionbook+xml' })
}

/** The first .fb2 inside an .fb2.zip, or null. */
export async function unzipFb2(zip: Blob): Promise<Blob | null> {
  const { configure, ZipReader, BlobReader, BlobWriter } = await import('../vendor/foliate/vendor/zip.js')
  configure({ useWebWorkers: false })
  const reader = new ZipReader(new BlobReader(zip))
  try {
    const entries = await reader.getEntries()
    const entry = entries.find((e) => !e.directory && e.filename.toLowerCase().endsWith('.fb2')) ?? entries.find((e) => !e.directory)
    return entry?.getData ? await entry.getData(new BlobWriter('application/x-fictionbook+xml')) : null
  } finally {
    await reader.close()
  }
}
