// Route table. The root gate sends people to first-run setup, the profile picker
// or the app depending on the session; admin-only sections are guarded too.
import { lazy, Suspense } from 'react'
import { Navigate, Outlet, createBrowserRouter, useLocation, type RouteObject } from 'react-router'
import { Button } from '../ui/Button'
import { Icon } from '../components/Icon'
import { LogoMark } from '../components/Logo'
import { ComingSoon } from '../pages/ComingSoon'
import { Home } from '../pages/Home'
import { ItemDetail } from '../pages/ItemDetail'
import { Collection } from '../pages/Collection'
import { Movies } from '../pages/Movies'
import { OfflineHome } from '../pages/OfflineHome'
import { offlineAvailable } from '../offline/store'
import { AudiobookDetail } from '../pages/books/AudiobookDetail'
import { BookDetail } from '../pages/books/BookDetail'
import { BooksGrid } from '../pages/books/BooksGrid'
import { BooksHome } from '../pages/books/BooksHome'
import { ComicSeries } from '../pages/books/ComicSeries'
import { Album } from '../pages/music/Album'
import { Albums } from '../pages/music/Albums'
import { Artist } from '../pages/music/Artist'
import { Artists } from '../pages/music/Artists'
import { MusicHome } from '../pages/music/MusicHome'
import { Playlist } from '../pages/music/Playlist'
import { Playlists } from '../pages/music/Playlists'
import { Songs } from '../pages/music/Songs'
import { Series } from '../pages/Series'
import { ProfilePicker } from '../pages/ProfilePicker'
import { Search } from '../pages/Search'
import { Setup } from '../pages/Setup'
import { session, useSession } from '../state/session'
import { ChunkBoundary } from './ChunkBoundary'
import { chunk, lazyFrom } from './lazyChunk'
import { FullSectionPrefetch, fullManageRoutes, fullNoKidsRoutes, fullPlayerRoutes, fullShellRoutes } from './routesFull'
import { Shell } from './Shell'
import styles from './App.module.css'
import { useT } from '../i18n'

function Splash() {
  const t = useT()
  return (
    <div className={styles.splash} aria-busy="true" aria-label={t('common.loading')}>
      <LogoMark size={64} />
    </div>
  )
}

function Offline() {
  const t = useT()
  return (
    <div className={styles.splash} role="alert">
      <Icon name="cloud" size={48} strokeWidth={1.4} />
      <h1 className={styles.offlineTitle}>{t('common.offlineServer')}</h1>
      <p className={styles.offlineText}>{t('app.offline.hint')}</p>
      <Button variant="secondary" onClick={() => void session.bootstrap()}>
        {t('common.retry')}
      </Button>
    </div>
  )
}

// Route elements are built once: placeholders read their text at render time.
function MusicSoon() {
  const t = useT()
  return <ComingSoon title={t('common.music')} icon="music" />
}
function BooksSoon() {
  const t = useT()
  return <ComingSoon title={t('common.books')} icon="book" />
}
function NotFound() {
  const t = useT()
  return <ComingSoon title={t('app.notFound.title')} icon="info" text={t('app.notFound.text')} />
}

// The player (and hls.js with it) loads only when something is played.
const Player = lazy(() => import('../pages/Player').then((m) => ({ default: m.Player })))
const PlayerRoute = () => (
  <Suspense fallback={<div className={styles.playerFallback} />}>
    <Player />
  </Suspense>
)

// «Файлы» load on first visit too (Finder-style browser of the media disk).
const FilesPage = lazy(() => import('../pages/files/FilesPage').then((m) => ({ default: m.FilesPage })))
const FilesRoute = () => (
  <ChunkBoundary>
    <FilesPage />
  </ChunkBoundary>
)

// «Управление» (admins): one chunk with the library's sections; the full
// build's own sections are another (routesFull.tsx), asked for at the same
// time when «Управление» opens on one of them (a section is rendered inside
// «Управление», so its chunk would otherwise wait for this one).
// A section inside «Управление» (same chunk) draws at once once «Управление» is in (lazyChunk).
const manage = chunk(() => import('../pages/manage'))
const section = (name: 'Manage' | 'Files' | 'Stats' | 'Profiles' | 'Settings' | 'Requests' | 'Migrate' | 'Import') => {
  const C = lazyFrom(manage, (m) => m[name])
  return (
    <ChunkBoundary>
      {!__LIBRARY_EDITION__ && name === 'Manage' && <FullSectionPrefetch />}
      <Suspense fallback={null}>
        <C />
      </Suspense>
    </ChunkBoundary>
  )
}

// «Мои просьбы» (kids) load on first visit too.
const MyRequests = lazy(() => import('../pages/MyRequests'))

// The reader (foliate-js, pdf.js; comics) loads only when a book is opened.
const ReadRoute = lazy(() => import('../reader/ReadRoute').then((m) => ({ default: m.ReadRoute })))
// The design system's gallery (/dev/gallery): the import sits behind DEV so a
// build has no such chunk at all.
const Gallery = import.meta.env.DEV ? lazy(() => import('../ui/gallery/Gallery').then((m) => ({ default: m.Gallery }))) : () => null
const GalleryRoute = () => (
  <Suspense fallback={null}>
    <Gallery />
  </Suspense>
)
const ReaderRoute = () => (
  <ChunkBoundary>
    <ReadRoute />
  </ChunkBoundary>
)

const AUTH_PATHS = ['/welcome', '/profiles']

function Gate() {
  const { status } = useSession()
  const { pathname, search } = useLocation()
  if (status === 'loading') return <Splash />
  if (status === 'offline') {
    // saved on this device: listen and read without the server (the reader opens full window)
    if (!offlineAvailable()) return <Offline />
    return pathname.startsWith('/read/') ? <Outlet /> : <OfflineHome />
  }
  if (status === 'setup') return pathname === '/welcome' ? <Outlet /> : <Navigate to="/welcome" replace />
  if (status === 'signed-out') {
    return pathname === '/profiles' ? (
      <Outlet />
    ) : (
      <Navigate to="/profiles" replace state={{ from: AUTH_PATHS.includes(pathname) ? '/' : pathname + search }} />
    )
  }
  return AUTH_PATHS.includes(pathname) ? <Navigate to="/" replace /> : <Outlet />
}

/** Kid profiles have no file browser. */
function NoKids() {
  const { user } = useSession()
  return user?.isKid ? <Navigate to="/" replace /> : <Outlet />
}

/** «Мои просьбы» is for the profiles that have «Попросить» or «Просьбы» on this server. */
function RequestsOnly() {
  const { user } = useSession()
  return user?.canRequest || user?.requests ? <Outlet /> : <Navigate to="/" replace />
}

function AdminOnly() {
  const { user } = useSession()
  return user?.isAdmin ? <Outlet /> : <Navigate to="/" replace />
}

export const routes: RouteObject[] = [
  {
    element: <Gate />,
    children: [
      { path: '/welcome', element: <Setup /> },
      { path: '/profiles', element: <ProfilePicker /> },
      // The player covers the whole window: no navigation around it.
      { path: '/watch/:id', element: <PlayerRoute /> },
      // The reader too; the music engine keeps playing.
      { path: '/read/:id', element: <ReaderRoute /> },
      // the sources build's full-window players (none in the core build)
      ...(__LIBRARY_EDITION__ ? [] : fullPlayerRoutes()),
      {
        element: <Shell />,
        children: [
          { index: true, element: <Home /> },
          { path: 'search', element: <Search /> },
          // the design system's gallery: dev server only, never in a build
          ...(import.meta.env.DEV ? [{ path: 'dev/gallery', element: <GalleryRoute /> }] : []),
          { path: 'offline', element: <OfflineHome /> },
          { path: 'movies', element: <Movies /> },
          { path: 'series', element: <Series /> },
          {
            path: 'music',
            children: [
              { index: true, element: <MusicHome /> },
              { path: 'albums', element: <Albums /> },
              { path: 'album/:id', element: <Album /> },
              { path: 'artists', element: <Artists /> },
              { path: 'artist/:id', element: <Artist /> },
              { path: 'songs', element: <Songs /> },
              { path: 'playlists', element: <Playlists /> },
              { path: 'playlist/:id', element: <Playlist /> },
              { path: '*', element: <MusicSoon /> },
            ],
          },
          {
            path: 'books',
            children: [
              { index: true, element: <BooksHome /> },
              { path: 'all/:kind', element: <BooksGrid /> },
              { path: 'comic/:id', element: <ComicSeries /> },
              { path: 'audiobook/:id', element: <AudiobookDetail /> },
              { path: ':id', element: <BookDetail /> },
              { path: '*', element: <BooksSoon /> },
            ],
          },
          { path: 'item/:id', element: <ItemDetail /> },
          { path: 'item/:id/collection', element: <Collection /> },
          // TMDB title pages (none in the core build)
          ...(__LIBRARY_EDITION__ ? [] : fullShellRoutes()),
          {
            element: <RequestsOnly />,
            children: [
              {
                path: 'requests',
                element: (
                  <ChunkBoundary>
                    <MyRequests />
                  </ChunkBoundary>
                ),
              },
            ],
          },
          {
            element: <NoKids />,
            children: [
              { path: 'files/*', element: <FilesRoute /> },
              ...(__LIBRARY_EDITION__ ? [] : fullNoKidsRoutes()),
            ],
          },
          {
            element: <AdminOnly />,
            children: [
              {
                path: 'manage',
                element: section('Manage'),
                children: [
                  ...(__LIBRARY_EDITION__ ? [] : fullManageRoutes()),
                  { path: 'files', element: section('Files') },
                  { path: 'stats', element: section('Stats') },
                  { path: 'profiles', element: section('Profiles') },
                  { path: 'settings', element: section('Settings') },
                  { path: 'requests', element: section('Requests') },
                  { path: 'migrate', element: section('Migrate') },
                  { path: 'import', element: section('Import') },
                ],
              },
            ],
          },
          {
            path: '*',
            element: <NotFound />,
          },
        ],
      },
    ],
  },
]

export const createRouter = () => createBrowserRouter(routes)
