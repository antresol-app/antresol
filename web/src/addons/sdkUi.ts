// The SDK's `ui`: the design system (src/ui) and the core's video player.
// `Player` is VideoPlayer behind React.lazy: its module (and hls.js after
// it) loads on the first render of a Player, never with the app or with an
// addon's bundle (npm run build checks: scripts/lazy-player.mjs).
import { lazy } from 'react'

export * from '../ui'
export type { StreamPlayback, UpNext, VideoPlayerProps as PlayerProps } from '../player/VideoPlayer'

/**
 * The core's full-window video player. A stream (an addon's source
 * through its proxy) passes `stream` (StreamPlayback) and `start`, which
 * answers with the play answer's shape (`mode: 'stream'`, its `session`).
 * Render it inside a Suspense boundary: the first render loads it.
 */
export const Player = lazy(() => import('../player/VideoPlayer').then((m) => ({ default: m.VideoPlayer })))
