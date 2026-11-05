// Which audio codecs this browser's <audio> decodes, in the server's names
// (the `accept` list of /api/tracks/{id}/stream). useCaps().audioCodecs is
// not used: it asks <video> and MSE and knows neither ALAC nor Vorbis.

const PROBES: [codec: string, mime: string][] = [
  ['flac', 'audio/flac'],
  ['mp3', 'audio/mpeg'],
  ['aac', 'audio/mp4; codecs="mp4a.40.2"'],
  ['alac', 'audio/mp4; codecs="alac"'],
  ['opus', 'audio/ogg; codecs="opus"'],
  ['vorbis', 'audio/ogg; codecs="vorbis"'],
]

/** Codecs answered with "maybe" or "probably" by canPlayType. */
export function audioAccept(): string[] {
  let el: HTMLAudioElement
  try {
    el = new Audio()
  } catch {
    return ['mp3', 'aac']
  }
  return PROBES.filter(([, mime]) => {
    try {
      return el.canPlayType(mime) !== ''
    } catch {
      return false
    }
  }).map(([codec]) => codec)
}

let cached: string[] | null = null

/** audioAccept(), measured once per page. */
export function acceptedCodecs(): string[] {
  if (!cached) cached = audioAccept()
  // Something the server can always transcode to.
  return cached.length > 0 ? cached : ['aac']
}
