// Signed-in layout: floating navigation, page content, mini player. The
// audio engine is a module singleton, so music keeps playing across routes.
import { useCallback, useEffect, useState } from 'react'
import { Outlet, useLocation } from 'react-router'
import { TopNav } from '../components/TopNav'
import { useActiveDownloads } from '../components/DownloadsBadge'
import { MiniPlayer } from '../player/MiniPlayer'
import { AudioScreenStop } from '../player/AudioScreenStop'
import { NowPlaying } from '../player/NowPlaying'
import { Notice } from '../components/Notice'
import { useAudioSelect } from '../player/audio/useAudio'
import { useSession } from '../state/session'
import { useLang, useT } from '../i18n'
import styles from './Shell.module.css'

// the admin's downloads badge (none in the core build)
const useDownloads = __LIBRARY_EDITION__ ? (_enabled: boolean) => 0 : useActiveDownloads

export function Shell() {
  // «Сейчас играет», opened from the mini player.
  const [full, setFull] = useState(false)
  const openFull = useCallback(() => setFull(true), [])
  const closeFull = useCallback(() => setFull(false), [])
  const { user } = useSession()
  const { pathname } = useLocation()
  const downloads = useDownloads(Boolean(user?.isAdmin))
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

  if (!user) return null
  return (
    <>
      <a href="#main" className="skip-link">
        {t('app.skipToContent')}
      </a>
      <TopNav user={user} downloads={downloads} />
      <main id="main" tabIndex={-1} className={styles.main} data-mini={mini ? 'on' : undefined}>
        <Outlet key={lang} />
      </main>
      <MiniPlayer onExpand={openFull} />
      <NowPlaying open={full} onClose={closeFull} />
      <Notice />
      <AudioScreenStop />
    </>
  )
}
