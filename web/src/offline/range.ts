// HTTP Range over a whole saved file: the service worker answers <audio>'s
// `Range: bytes=…` requests for a track or audiobook kept on the device.

/** The byte range of `bytes=a-b`, `bytes=a-` or `bytes=-n` in a file of `size` bytes; null when unsatisfiable. */
export function parseRange(header: string, size: number): { start: number; end: number } | null {
  const m = /^\s*bytes\s*=\s*(\d*)\s*-\s*(\d*)\s*(?:,.*)?$/i.exec(header)
  if (!m || size <= 0) return null
  const [, a = '', b = ''] = m
  if (a === '' && b === '') return null
  let start: number
  let end: number
  if (a === '') {
    // suffix: the last n bytes
    const n = Number(b)
    if (n <= 0) return null
    start = Math.max(0, size - n)
    end = size - 1
  } else {
    start = Number(a)
    end = b === '' ? size - 1 : Math.min(Number(b), size - 1)
  }
  if (!Number.isFinite(start) || !Number.isFinite(end) || start >= size || end < start) return null
  return { start, end }
}

/** 206 with the requested bytes of `full`, or 416 when the range does not fit. */
export function rangeResponse(full: Blob, header: string, type: string): Response {
  const size = full.size
  const r = parseRange(header, size)
  if (!r) {
    return new Response(null, { status: 416, headers: { 'Content-Range': `bytes */${size}` } })
  }
  const body = full.slice(r.start, r.end + 1, type)
  return new Response(body, {
    status: 206,
    headers: {
      'Content-Type': type,
      'Content-Range': `bytes ${r.start}-${r.end}/${size}`,
      'Content-Length': String(r.end - r.start + 1),
      'Accept-Ranges': 'bytes',
    },
  })
}
