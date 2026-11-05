// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2026 Antresol Contributors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// The reading file as foliate wants it, shared by the web reader (Reader)
// and the native app's reader bundle (reader/native): the name tells FB2
// from FB2.ZIP and MOBI from AZW3, and an FB2 is unzipped and re-encoded
// from undeclared Windows-1251 first.
import { fb2ToUtf8, unzipFb2 } from './fb2text'

const ZIP = [0x50, 0x4b, 0x03, 0x04]

/**
 * The file name foliate needs: it tells FB2 from FB2.ZIP and MOBI from AZW3
 * by name as well as by content. An FB2 that is a ZIP is a .fb2.zip.
 */
export async function bookFileName(format: string, blob: Blob): Promise<string> {
  const head = new Uint8Array(await blob.slice(0, 4).arrayBuffer())
  const zip = ZIP.every((b, i) => head[i] === b)
  switch (format) {
    case 'FB2':
      return zip ? 'book.fb2.zip' : 'book.fb2'
    case 'MOBI':
      return 'book.mobi'
    case 'AZW3':
      return 'book.azw3'
    default:
      return 'book.epub'
  }
}

/** The downloaded reading file (EPUB, FB2, MOBI, AZW3) ready for foliate's makeBook. */
export async function bookFile(format: string, downloaded: Blob): Promise<File> {
  let blob = downloaded
  let name = await bookFileName(format, blob)
  if (format === 'FB2') {
    // undeclared Windows-1251 → UTF-8 (foliate would decode it as UTF-8)
    const inner = name.endsWith('.zip') ? await unzipFb2(blob) : null
    if (inner) [blob, name] = [inner, 'book.fb2']
    if (name === 'book.fb2') blob = await fb2ToUtf8(blob)
  }
  return new File([blob], name, { type: blob.type })
}
