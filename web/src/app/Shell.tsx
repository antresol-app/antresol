// Signed-in layout: floating navigation, page content, mini player (none
// around an addon's fullWindow route, which covers the window). The
// audio engine is a module singleton, so music keeps playing across routes.
import { useCallback, useEffect, useState } from 'react'
import { Outlet, useLocation } from 'react-router'
import { TopNav } from '../components/TopNav'
import { MiniPlayer } from '../player/MiniPlayer'
import { AudioScreenStop } from '../player/AudioScreenStop'
import { NowPlaying } from '../player/NowPlaying'
import { WritePrompt } from '../components/accounts/WritePrompt'
import { ToastHost } from '../components/ToastHost'
import { UpdateBanner } from '../components/UpdateBanner'
import { useAudioSelect } from '../player/audio/useAudio'
import { useSession } from '../state/session'
import { useLang, useT } from '../i18n'
import { prefetchSections } from './chunks'
import { useFullWindowAddon } from '../addons/slots'
import { AddonWaysOutlet } from '../addons/watchWays'
import styles from './Shell.module.css'
import appStyles from './App.module.css'

export function Shell() {
  // «Сейчас играет», opened from the mini player.
  const [full, setFull] = useState(false)
  const openFull = useCallback(() => setFull(true), [])
  const closeFull = useCallback(() => setFull(false), [])
  const { user } = useSession()
  const { pathname } = useLocation()
  // The mini player floats over the bottom of the page: keep it clear.
  const mini = useAudioSelect((s) => s.queue.length > 0)
  const t = useT()
  // A new interface language remounts the page (it fetches again with the new
  // Accept-Language); the player and the navigation stay as they are.
  const lang = useLang()

  // New page starts at the top; back or a link from the queue closes «Сейчас играет» first.
  useEffect(() => {
    window.scrollTo(0, 0)
    setFull(false)
  }, [pathname])

  // the other sections' chunks, once this page is up
  useEffect(() => {
    if (typeof requestIdleCallback !== 'function') {
      const id = setTimeout(prefetchSections, 2000)
      return () => clearTimeout(id)
    }
    const id = requestIdleCallback(prefetchSections, { timeout: 4000 })
    return () => cancelIdleCallback(id)
  }, [])

  // an addon's fullWindow route (its player) covers the window: none of
  // the shell around it; the shell itself stays mounted, so its parts keep
  // their state across the switch. `pending`: the player's black fallback
  // until the addon registers (a cold load of a route it had last time).
  const fullWindow = useFullWindowAddon()

  if (!user) return null
  if (fullWindow !== 'off')
    return (
      <>
        {fullWindow === 'pending' ? <div className={appStyles.playerFallback} /> : null}
        <Outlet key={lang} />
        <AddonWaysOutlet />
      </>
    )
  return (
    <>
      <a href="#main" className="skip-link">
        {t('app.skipToContent')}
      </a>
      <TopNav user={user} />
      <UpdateBanner />
      <main id="main" tabIndex={-1} className={styles.main} data-mini={mini ? 'on' : undefined}>
        <Outlet key={lang} />
      </main>
      <MiniPlayer onExpand={openFull} />
      <NowPlaying open={full} onClose={closeFull} />
      <ToastHost />
      {/* the SDK's useWatchWays: its ways' availability and sheets */}
      <AddonWaysOutlet />
      <WritePrompt />
      <AudioScreenStop />
    </>
  )
}
