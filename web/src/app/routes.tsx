// Route table. The root gate sends people to first-run setup, the profile picker
// or the app depending on the session; admin-only sections are guarded too.
import { lazy, Suspense, useEffect, type ComponentType } from 'react'
import { Navigate, Outlet, createBrowserRouter, useLocation, type RouteObject } from 'react-router'
import { Button } from '../ui/Button'
import { Icon } from '../components/Icon'
import { LogoMark } from '../components/Logo'
import { ComingSoon } from '../pages/ComingSoon'
import { Home } from '../pages/Home'
import { ItemDetail } from '../pages/ItemDetail'
import { OfflineHome } from '../pages/OfflineHome'
import { offlineAvailable } from '../offline/store'
import { session, useSession } from '../state/session'
import { useFullWindowAddon } from '../addons/slots'
import { ChunkBoundary } from './ChunkBoundary'
import { auth, books, browse, calendar, music, search, tv } from './chunks'
import { chunk, lazyFrom, type Chunk } from './lazyChunk'
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

/** A page of a lazily loaded chunk, with the chunk's loading and failure states. */
function page<M>(c: Chunk<M>, pick: (m: M) => ComponentType) {
  const C = lazyFrom(c, pick)
  return (
    <ChunkBoundary>
      <C />
    </ChunkBoundary>
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
// An addon's video (/play/input/:id): the same player, its own small page.
const InputPlayer = lazy(() => import('../pages/InputPlayer').then((m) => ({ default: m.InputPlayer })))
const InputPlayerRoute = () => (
  <Suspense fallback={<div className={styles.playerFallback} />}>
    <InputPlayer />
  </Suspense>
)

// «Файлы» load on first visit too (Finder-style browser of the media disk).
const FilesPage = lazy(() => import('../pages/files/FilesPage').then((m) => ({ default: m.FilesPage })))
const FilesRoute = () => (
  <ChunkBoundary>
    <FilesPage />
  </ChunkBoundary>
)

// «Управление» (admins): one chunk with the library's sections.
// A section inside «Управление» (same chunk) draws at once once «Управление» is in (lazyChunk).
const manage = chunk(() => import('../pages/manage'))
const section = (name: 'Manage' | 'Files' | 'Stats' | 'Profiles' | 'Settings' | 'Requests' | 'Migrate' | 'Import' | 'Addons' | 'Health' | 'Tasks') => {
  const C = lazyFrom(manage, (m) => m[name])
  return (
    <ChunkBoundary>
      <Suspense fallback={null}>
        <C />
      </Suspense>
    </ChunkBoundary>
  )
}

// «Управление каналами»: an admin's are in «Настройки» (/manage/channels,
// the old address leads there, «Новый канал» too); a profile that is not an
// admin manages its own channels from «ТВ».
const tvManageOwn = page(tv, (m) => m.TvManage)
function TvManageRoute() {
  const { user } = useSession()
  const { search } = useLocation()
  if (user?.isAdmin) return <Navigate to={`/manage/channels${search}`} replace />
  return tvManageOwn
}
function ToManageChannels() {
  const { pathname, search } = useLocation()
  return <Navigate to={pathname.replace(/^\/tv\/manage/, '/manage/channels') + search} replace />
}

// An addon's page (/addons/<id>/<path>, not for kids): its own small chunk.
const AddonPage = lazy(() => import('../pages/AddonPage'))
// A fullWindow route (an addon's player) waits on the player's black
// screen, as the core players do, never on the section spinner.
function AddonRoute() {
  const fullWindow = useFullWindowAddon() !== 'off'
  return (
    <ChunkBoundary fallback={fullWindow ? <div className={styles.playerFallback} /> : undefined}>
      <AddonPage />
    </ChunkBoundary>
  )
}
// «Мои запросы» (kids) load on first visit too.
const MyRequests = lazy(() => import('../pages/MyRequests'))
// a profile's own «Настройки» (every profile)
const ProfileSettings = lazy(() => import('../pages/ProfileSettings'))
// its «Аккаунты и синхронизация» (every adult profile; a kid is sent back to the settings)
const AccountsSettings = lazy(() => import('../pages/AccountsSettings'))
const ApiKeysSettings = lazy(() => import('../pages/ApiKeysSettings'))
// «Вход на телевизоре» (/pair?code=…, the TV's QR code)
const Pair = lazy(() => import('../pages/Pair'))

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
  const { status, user } = useSession()
  const { pathname, search } = useLocation()
  // the profile's addons (once per profile: sign-in and a profile switch load them anew);
  // the loader is its own chunk, off the first load
  const addonUser = status === 'signed-in' && user ? user : null
  useEffect(() => {
    if (addonUser)
      void import('../addons/loader')
        .then((m) => m.initAddons(addonUser))
        .catch((e: unknown) => console.warn('antresol: addon loader unavailable', e))
  }, [addonUser?.id])
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

/** «Мои запросы» is for the profiles that have «Попросить» or «Запросы» on this server. */
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
      { path: '/welcome', element: page(auth, (m) => m.Setup) },
      { path: '/profiles', element: page(auth, (m) => m.ProfilePicker) },
      // The steps after the first administrator: only while the server says
      // they are pending (the page asks), only for an administrator.
      { element: <AdminOnly />, children: [{ path: '/setup/wizard', element: page(auth, (m) => m.SetupWizard) }] },
      // The player covers the whole window: no navigation around it.
      { path: '/watch/:id', element: <PlayerRoute /> },
      // An addon's video too.
      { path: '/play/input/:id', element: <InputPlayerRoute /> },
      // A live channel too («ТВ»).
      { path: '/tv/:id', element: page(tv, (m) => m.LivePlayer) },
      // The reader too; the music engine keeps playing.
      { path: '/read/:id', element: <ReaderRoute /> },
      {
        element: <Shell />,
        children: [
          { index: true, element: <Home /> },
          { path: 'search', element: page(search, (m) => m.Search) },
          // the design system's gallery: dev server only, never in a build
          ...(import.meta.env.DEV ? [{ path: 'dev/gallery', element: <GalleryRoute /> }] : []),
          { path: 'offline', element: <OfflineHome /> },
          {
            path: 'settings',
            element: (
              <ChunkBoundary>
                <ProfileSettings />
              </ChunkBoundary>
            ),
          },
          {
            path: 'settings/accounts',
            element: (
              <ChunkBoundary>
                <AccountsSettings />
              </ChunkBoundary>
            ),
          },
          {
            path: 'settings/api-keys',
            element: (
              <ChunkBoundary>
                <ApiKeysSettings />
              </ChunkBoundary>
            ),
          },
          {
            path: 'pair',
            element: (
              <ChunkBoundary>
                <Pair />
              </ChunkBoundary>
            ),
          },
          { path: 'movies', element: page(browse, (m) => m.Movies) },
          { path: 'series', element: page(browse, (m) => m.Series) },
          { path: 'tv', element: page(tv, (m) => m.Tv) },
          { path: 'calendar', element: page(calendar, (m) => m.Calendar) },
          {
            path: 'music',
            children: [
              { index: true, element: page(music, (m) => m.MusicHome) },
              { path: 'albums', element: page(music, (m) => m.Albums) },
              { path: 'album/:id', element: page(music, (m) => m.Album) },
              { path: 'artists', element: page(music, (m) => m.Artists) },
              { path: 'artist/:id', element: page(music, (m) => m.Artist) },
              { path: 'songs', element: page(music, (m) => m.Songs) },
              { path: 'playlists', element: page(music, (m) => m.Playlists) },
              { path: 'playlist/:id', element: page(music, (m) => m.Playlist) },
              { path: '*', element: <MusicSoon /> },
            ],
          },
          {
            path: 'books',
            children: [
              { index: true, element: page(books, (m) => m.BooksHome) },
              { path: 'all/:kind', element: page(books, (m) => m.BooksGrid) },
              { path: 'comic/:id', element: page(books, (m) => m.ComicSeries) },
              { path: 'audiobook/:id', element: page(books, (m) => m.AudiobookDetail) },
              { path: ':id', element: page(books, (m) => m.BookDetail) },
              { path: '*', element: <BooksSoon /> },
            ],
          },
          { path: 'item/:id', element: <ItemDetail /> },
          { path: 'item/:id/collection', element: page(browse, (m) => m.Collection) },
          // a TMDB title that may not be in the library (a kid's has «Попросить»)
          { path: 'title/:kind/:tmdbId', element: <ItemDetail /> },
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
              // an admin's channels are managed in «Настройки» (/manage/channels)
              { path: 'tv/manage', element: <TvManageRoute /> },
              // the server lists an addon for the profiles it is for (admins, or
              // adults too); a fullWindow route covers the window like the
              // players above (the shell draws none of itself: useFullWindowAddon)
              { path: 'addons/:addonId/*', element: <AddonRoute /> },
            ],
          },
          {
            element: <AdminOnly />,
            children: [
              // the old addresses of the admin's channel pages
              { path: 'tv/manage/*', element: <ToManageChannels /> },
              {
                path: 'manage',
                element: section('Manage'),
                children: [
                  { path: 'files', element: section('Files') },
                  { path: 'stats', element: section('Stats') },
                  { path: 'profiles', element: section('Profiles') },
                  { path: 'settings/:group?', element: section('Settings') },
                  { path: 'requests', element: section('Requests') },
                  { path: 'migrate', element: section('Migrate') },
                  { path: 'import', element: section('Import') },
                  { path: 'addons', element: section('Addons') },
                  { path: 'channels', element: page(tv, (m) => m.TvManage) },
                  { path: 'channels/sources', element: page(tv, (m) => m.SourcesPage) },
                  { path: 'channels/sources/:id', element: page(tv, (m) => m.SourceChannels) },
                  { path: 'channels/franchises', element: page(tv, (m) => m.FranchisesPage) },
                  { path: 'health', element: section('Health') },
                  { path: 'tasks', element: section('Tasks') },
                  // «Обновления» became part of «Состояние»
                  { path: 'updates', element: <Navigate to="/manage/health" replace /> },
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
