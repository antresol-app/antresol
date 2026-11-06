// Which HLS player a browser gets: its own (Safari, iOS — they never
// download hls.js, not even into the service worker's cache) or hls.js over
// Media Source Extensions. Decided without loading hls.js.

const isSafari = () => typeof navigator !== 'undefined' && /^((?!chrome|android|crios|fxios).)*safari/i.test(navigator.userAgent)

/** What hls.js needs (its own isSupported() without loading it): Media Source Extensions with H.264/AAC. */
export function mseSupported(): boolean {
  const w = globalThis as typeof globalThis & { ManagedMediaSource?: typeof MediaSource; WebKitMediaSource?: typeof MediaSource }
  const MS = w.MediaSource ?? w.ManagedMediaSource ?? w.WebKitMediaSource
  return typeof MS?.isTypeSupported === 'function' && MS.isTypeSupported('video/mp4; codecs="avc1.42E01E,mp4a.40.2"')
}

/** The browser plays HLS itself: it can, and it is Safari (better there) or has no MSE for hls.js. */
export function nativeHls(video: HTMLVideoElement): boolean {
  const can = video.canPlayType('application/vnd.apple.mpegurl') !== ''
  return can && (isSafari() || !mseSupported())
}
