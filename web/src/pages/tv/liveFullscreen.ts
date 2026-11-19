// Full screen for the live player. A desktop browser (and an iPad's Safari)
// puts the player's own element in full screen, so its chrome, subtitles
// and menus come along; an iPhone's Safari has no element full screen at
// all — only the video's own (webkitEnterFullscreen), the system player
// with its own controls, AirPlay and picture in picture.

export type FsVideo = HTMLVideoElement & {
  webkitSupportsFullscreen?: boolean
  webkitDisplayingFullscreen?: boolean
  webkitEnterFullscreen?: () => void
  webkitExitFullscreen?: () => void
}
type FsDocument = Document & { webkitFullscreenEnabled?: boolean; webkitFullscreenElement?: Element | null; webkitExitFullscreen?: () => void }
type FsElement = HTMLElement & { webkitRequestFullscreen?: () => void }

const doc = () => document as FsDocument

/** The element in full screen (the standard or WebKit's), else null. */
export const fullscreenElement = (): Element | null => doc().fullscreenElement ?? doc().webkitFullscreenElement ?? null

/** The page may put an element in full screen (not an iPhone's Safari). */
export const elementFullscreen = (): boolean => typeof document !== 'undefined' && Boolean(doc().fullscreenEnabled || doc().webkitFullscreenEnabled)

/** The video has a full screen of its own (an iPhone's Safari). */
export const videoFullscreen = (video: FsVideo | null | undefined): boolean => Boolean(video && typeof video.webkitEnterFullscreen === 'function')

/** Some full screen is possible: the element's, else the video's own. */
export const canFullscreen = (video: FsVideo | null | undefined): boolean => elementFullscreen() || videoFullscreen(video)

/** Whether the player is in full screen now (its element, or the video's own). */
export const isFullscreen = (video: FsVideo | null | undefined): boolean => Boolean(fullscreenElement() || video?.webkitDisplayingFullscreen)

/**
 * Full screen on or off: the player's element when the browser can (its
 * chrome comes along), else the video's own (an iPhone). Returns which one
 * was asked for ('element' | 'video'), 'exit', or null (none possible).
 */
export function toggleFullscreen(root: HTMLElement | null, video: FsVideo | null): 'element' | 'video' | 'exit' | null {
  if (fullscreenElement()) {
    if (document.exitFullscreen) void document.exitFullscreen().catch(() => undefined)
    else doc().webkitExitFullscreen?.()
    return 'exit'
  }
  if (video?.webkitDisplayingFullscreen) {
    video.webkitExitFullscreen?.()
    return 'exit'
  }
  const el = root as FsElement | null
  if (el && elementFullscreen()) {
    if (el.requestFullscreen) void el.requestFullscreen().catch(() => undefined)
    else el.webkitRequestFullscreen?.()
    return 'element'
  }
  if (video && videoFullscreen(video)) {
    try {
      video.webkitEnterFullscreen!()
    } catch {
      // before the video has its first frame Safari refuses: nothing happens
      return null
    }
    return 'video'
  }
  return null
}

/** Calls on() whenever full screen starts or ends (the element's or the video's). */
export function onFullscreenChange(video: FsVideo | null, on: () => void): () => void {
  document.addEventListener('fullscreenchange', on)
  document.addEventListener('webkitfullscreenchange', on)
  video?.addEventListener('webkitbeginfullscreen', on)
  video?.addEventListener('webkitendfullscreen', on)
  return () => {
    document.removeEventListener('fullscreenchange', on)
    document.removeEventListener('webkitfullscreenchange', on)
    video?.removeEventListener('webkitbeginfullscreen', on)
    video?.removeEventListener('webkitendfullscreen', on)
  }
}
