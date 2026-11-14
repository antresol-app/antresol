// API contracts shared by the whole web app. Field names mirror the Go JSON tags.

import type { AnnotationColor } from '../reader/highlights'

export type AvatarId = `grad-${1 | 2 | 3 | 4 | 5 | 6 | 7 | 8}` | (string & {})

export interface Profile {
  id: number
  name: string
  avatar: AvatarId
  hasPassword: boolean
}

export interface User extends Profile {
  isAdmin: boolean
  isKid: boolean
  maxRating: string | null
  /** «Скрывать прогресс просмотра»: left out of the others' «Семья» (a kid's is the admin's to change). */
  hideProgress?: boolean
  /** The profile's Send to Kindle address, set by an admin (empty: none). */
  kindleEmail?: string
  /** Set by /api/me, login and setup: this device is outside the home network. */
  remote?: boolean
  /** «Смотреть онлайн» works for this profile on this server (never for a kid). */
  canWatchOnline?: boolean
  /** A kid profile may ask for titles here («Попросить»). */
  canRequest?: boolean
  /** An admin has kids' requests to answer here («Управление → Просьбы»). */
  requests?: boolean
  /** The profile may bring titles from outside the library here (another build; never a kid). */
  canAcquire?: boolean
  /** The server serves its library only (VAULT_LIBRARY_ONLY): no «Найти и
   * добавить», «Следить», downloads, sources, import or migration. */
  libraryOnly?: boolean
  /** «Общая медиатека» (all) or «Моя медиатека» (mine): the library this
   * profile's pages show (always all for a kid; older servers send none). */
  libraryScope?: LibraryScope
}

/** Which library a page lists: the shared one, or the titles this profile brought. */
export type LibraryScope = 'all' | 'mine'

export interface SetupState {
  needsSetup: boolean
  /** Outside the home network: no first-run setup and no passwordless profiles. */
  remote?: boolean
}

/** GET /api/about: what runs on the server and where its source is (AGPL §13). */
export interface About {
  version: string
  commit: string
  /** SPDX expression: AGPL-3.0-or-later. */
  license: string
  /** The source of this very build; absent when the build offers no link. */
  sourceUrl?: string
}

export interface Health {
  ok: boolean
  version: string
  /** Stable id of this server (apps tell servers apart by it). */
  serverId: string
}

/** A Vault app signed in to the profile («Устройства»). */
export interface Device {
  id: number
  /** 'web': a browser signed in to the profile (an older server sends none: an app). */
  kind?: 'native' | 'web'
  name: string
  model: string
  version: string
  createdAt: string
  lastSeen: string
  /** The device making this request. */
  current: boolean
  /** The profile signed in on it (an older server sends none). */
  userId?: number
  userName?: string
  userAvatar?: string
}

export type CardKind =
  | 'movie'
  | 'series'
  | 'season'
  | 'episode'
  | 'artist'
  | 'album'
  | 'track'
  | 'book'
  | 'audiobook'
  | 'comic'
  | 'comic_volume'
  | 'playlist'
  | (string & {})

/** A tile on a shelf, grid or in the hero. Images are /api/images/… URLs (bare names are resolved by imageUrl). */
export interface Card {
  id: number
  kind: CardKind
  title: string
  subtitle: string
  /** Omitted when unknown. */
  year?: number
  poster: string
  backdrop: string
  logo: string
  /** 0..1 */
  progress: number
  onServer: boolean
  /** Cards of GET /api/items/{id}/related: this profile finished the movie or every episode of the series. */
  watched?: boolean
  /** Facts worth a chip for the kind: FLAC 24/96, 4K, RU, EPUB. */
  badges?: string[]
  /** Episodes and seasons: the parent series. */
  seriesId?: number
  /** «Продолжить просмотр» of an online title (id is 0: route on tmdbKind and tmdbId). */
  online?: boolean
  tmdbKind?: 'movie' | 'series'
  tmdbId?: number
  /** Online series: the episode to resume (missing = 0). */
  season?: number
  episode?: number
  resumeSec?: number
  /**
   * Sources build: a title on its way into the library (GET /api/library/pending).
   * With id 0 the card is the download's own (tinted, opened by tmdbKind/tmdbId);
   * otherwise the progress sits on the library's card of that id.
   */
  pending?: PendingState
}

/** A download into the library behind a pending card. */
export interface PendingState {
  key: string
  /** `error` reaches admins only. */
  status: 'active' | 'waiting' | 'paused' | 'organizing' | 'error'
  done: number
  size: number
  /** active only: see TorrentStalled. */
  stalled?: TorrentStalled
  /** active only: see TorrentHint. */
  liveness?: TorrentLiveness
  trackerError?: string
  trackerErrorKind?: TrackerErrorKind
}

export interface Shelf {
  id: 'continue' | 'recent-movies' | 'recent-series' | 'watchlist' | (string & {})
  title: string
  cards: Card[]
}

export interface Home {
  hero: Card[]
  shelves: Shelf[]
}

export type ItemSort = 'recent' | 'title' | 'year'

export interface ItemsPage {
  items: Card[]
  total: number
}

/** The first audio index of the files lying beside the video (EXTERNAL_AUDIO_BASE + i). */
export const EXTERNAL_AUDIO_BASE = 2000

export interface Track {
  /** ffprobe index; audio files beside the video are 2000+ (EXTERNAL_AUDIO_BASE). */
  index: number
  codec: string
  lang: string
  title: string
  channels?: number
  default: boolean
  /** An audio file beside the video (a release's «RUS Sound»), played like the file's own tracks. */
  external?: boolean
  /** The external track's id: sent back as PlayRequest.audioSrc. */
  src?: string
  /** The voice-over studio its name or folder names («LostFilm»). */
  studio?: string
  /** «Очищено»: a voice-over with the original speech subtracted, made by the server from the file's track `cleanedFrom`. */
  kind?: 'cleaned'
  /** How it was cleaned: subtraction | ml. */
  method?: string
  cleanedFrom?: number
}

export interface SubTrack {
  /** ffprobe index; external subtitle files are 1000+. */
  index: number
  codec: string
  lang: string
  title: string
  forced: boolean
  default: boolean
  external: boolean
  burnIn: boolean
  url?: string
}

/** «Субтитры»: off (forced ones only) | foreign (when the audio is in none of the profile's languages) | always. */
export type SubMode = 'off' | 'foreign' | 'always'
export type SubSize = 's' | 'm' | 'l' | 'xl'
export type SubBackground = 'none' | 'translucent' | 'solid'

/** A profile's audio and subtitle defaults (GET /api/me/playback-prefs). */
export interface PlaybackPrefs {
  /** Lang codes, best first. */
  audioLangs: LangCode[]
  subMode: SubMode
  /** [] = the audio languages. */
  subLangs: LangCode[]
  subSize: SubSize
  subBackground: SubBackground
  /** The profile saved its own (false: the household's «Языки»). */
  custom: boolean
  /** «Выбирать очищенную озвучку»: the cleaned version of the chosen voice-over plays when there is one (an older server: absent). */
  preferCleaned?: boolean
}

/** «Уведомления на почту»: what a profile may hear about (another build adds the others). */
export type NotifyTopic = 'episodes' | 'available' | 'requests' | 'downloads'

/** A profile's notification settings (GET /api/me/notify, an admin's /api/users/{id}/notify). */
export interface NotifyPrefs {
  email: string
  /** The address was confirmed by its verification letter (code or link). */
  verified: boolean
  episodes: boolean
  available: boolean
  requests: boolean
  downloads: boolean
  /** Letters wait from 22:00 to 08:00 (server time). */
  quietHours: boolean
  /** The letters' language: ru | en; '' the server's. */
  lang: string
  /** The topics this profile may turn on. */
  topics: NotifyTopic[]
  /** The server has a mail account. */
  mailReady: boolean
  /** Letters carry links (the server knows its public address). */
  publicLinks: boolean
  /** The mail server refused the address for good: unconfirmed until confirmed again. */
  bounced: boolean
}

/** A track as it is remembered: the file's language tag and the track's title (a dub's studio). */
export interface TrackChoice {
  lang: string
  title: string
  /** The voice was its cleaned version (audio only; absent: the track itself). */
  kind?: 'cleaned'
}

/** What a profile chose in the episodes of a series. */
export interface SeriesTracks {
  audio: TrackChoice | null
  subtitle: TrackChoice | null
  subtitlesOff: boolean
}

/** GET /api/items/{id}/track-prefs: what the player needs before it starts. */
export interface TrackPrefs {
  prefs: PlaybackPrefs
  series: SeriesTracks | null
  /** «Субтитры раньше / позже» by file id, ms (positive: later). */
  offsets: Record<string, number>
}

export interface MediaFile {
  id: number
  name: string
  size: number
  durationMs?: number
  resolution?: string
  audio: Track[]
  subs: SubTrack[]
}

export interface Episode extends Card {
  seasonNo: number
  episodeNo: number
  overview?: string
  durationMs?: number
  watched: boolean
}

export interface Season {
  id: number
  number: number
  title: string
  episodes: Episode[]
}

export interface CastMember {
  name: string
  character?: string
  photo?: string
}

export interface ItemActions {
  play: boolean
  toServer: boolean
  toDevice: boolean
  watchNow: boolean
}

export interface ItemDetail extends Card {
  originalTitle?: string
  overview: string
  genres: string[]
  /** Minutes. */
  runtime?: number
  rating?: string
  cast: CastMember[]
  seasons: Season[]
  files: MediaFile[]
  /** Series: the episode to play next. */
  next?: Episode
  inWatchlist: boolean
  actions: ItemActions
  /** Movies and series: the TMDB id («Смотреть онлайн»; a series' seasons that are not on the server yet). */
  tmdbId?: number
  /** Movies and series: the IMDb id ("tt0816692"), the key of GET /api/ratings/{imdbId}. */
  imdbId?: string
  /**
   * «Следить»: the follow of this title, null when not followed. Left out
   * (no button) without a TMDB key, for kid profiles and for other kinds.
   */
  follow?: FollowRef | null
}

/** A season a series delete may be narrowed to (DeletePlan.seasons). */
export interface DeleteSeason {
  id: number
  number: number
  title: string
  episodes: number
  /** Bytes. */
  size: number
  /** What deleting just this season does besides (DeletePlan.notes). */
  notes: string[]
}

/** GET /api/items/{id}/delete (admin): what deleting the item would take away. */
export interface DeletePlan {
  id: number
  kind: string
  title: string
  /** Bytes of the files that go. */
  size: number
  files: number
  /** A series: its seasons, in order; empty for other kinds. */
  seasons: DeleteSeason[]
  /** What else happens, in plain words (the sources build: what stops). */
  notes: string[]
}

/** POST /api/items/{id}/delete (admin). */
export interface DeleteResult {
  items: number[]
  size: number
  trashed: number
  /** The item's parent (a season's series; 0 for none) and whether it is still in the library. */
  parentId: number
  parentShown: boolean
  /** What else to know, in plain words (files that reached a folder while it moved). */
  note?: string
}

/** GET /api/ratings/{imdbId}: what is known of a title; absent fields are unknown. */
export interface Ratings {
  /** IMDb's own ratings dataset: 1.0–10.0 and the number of votes. */
  imdb?: { rating: number; votes: number }
  /** Rotten Tomatoes via Wikidata, in percent: critics (Tomatometer) and the audience (Popcornmeter). */
  rottenTomatoes?: { tomatometer?: number; audience?: number }
}

/** The follow of a title page (ItemDetail.follow, TitleInfo.follow). */
export interface FollowRef {
  id: number
  active: boolean
  /** Profile that added it ('' = unknown or deleted): another adult's follow is shown, not changed. */
  addedBy?: string
}

/** GET /api/follow: a followed series (new episodes) or movie (once released). */
export interface Follow {
  id: number
  kind: 'series' | 'movie'
  tmdbId: number
  title: string
  /** Full TMDB poster URL, '' = none. */
  poster: string
  active: boolean
  /** RFC 3339; null = not checked yet. */
  lastChecked: string | null
  lastError: string
  /** Aired episodes (a released movie: 1) not in the library and not downloading. */
  missing: number
  /** Profile that added it ('' = unknown or deleted). */
  addedBy: string
  /** vault, or sonarr | radarr: taken over by «Переезд» (inactive until «Перейти на Vault»). */
  source?: 'vault' | 'sonarr' | 'radarr' | (string & {})
}

/** What a voice-over release covers of the title on the server (GET /api/items/{id}/voiceovers). */
export interface VoiceOverCoverage {
  /** Episodes of the server it has a track for, of `total` (a film: 1 of 1, `film`). */
  have: number
  total: number
  /** The release says which seasons (its name, or its file list: `exact`). */
  known: boolean
  exact: boolean
  film?: boolean
}

/** «Найти озвучку…»: a release with a voice-over of the title (`video`: a video release that may hold separate tracks). */
export interface VoiceOverCandidate {
  /** Names it in the search (POST takes it back). */
  id: string
  sourceId: number
  sourceName: string
  title: string
  studio: string
  studios: string[]
  seeders: number
  size: number
  infoHash?: string
  coverage: VoiceOverCoverage
  video?: boolean
}

export interface VoiceOvers {
  candidates: VoiceOverCandidate[]
  videoReleases: VoiceOverCandidate[]
  failed: { sourceId: number; name: string; message: string }[]
}

/** A voice-over's episode (season/episode absent: the film) and what became of its track. */
export interface VoiceOverEpisode {
  season?: number
  episode?: number
  fileId: number
  state?: 'downloading' | 'placed' | 'fitted' | 'unmeasured' | 'misfit' | 'skipped' | 'had'
  /** fitted: ms, positive = later on the video's timeline. */
  offsetMs?: number
  /** fitted: the speed correction (atempo; 1 or absent = none). */
  tempo?: number
}

/** POST /api/items/{id}/voiceovers/files: a release's file list as the episodes it covers. */
export interface VoiceOverPreview {
  id: string
  coverage: VoiceOverCoverage
  episodes: VoiceOverEpisode[]
  /** Why nothing can be taken from it. */
  message?: string
}

/** GET /api/items/{id}/voiceovers/downloads: a voice-over download of the title. */
export interface VoiceOverDownload {
  infoHash: string
  title: string
  studio: string
  status: string
  /** 0…1 */
  progress: number
  done: boolean
  episodes: VoiceOverEpisode[]
}

/** GET /api/subtitles/{fileId}/search: one OpenSubtitles result, best first. */
export interface SubtitleResult {
  subtitleId: number
  /** ISO 639-1. */
  lang: string
  release: string
  downloads: number
  /** Made for exactly this file (OpenSubtitles hash). */
  hashMatch: boolean
}

/** GET /api/search: every section of the library at once (songs up to 10, others 20). */
export interface SearchResult {
  /** Movies and series. */
  library: Card[]
  artists: Card[]
  albums: Card[]
  songs: Song[]
  books: Card[]
  audiobooks: Card[]
  comics: Card[]
}

/** What the browser can decode; sent with POST /api/play/{fileId}. */
export interface ClientCaps {
  videoCodecs: string[]
  audioCodecs: string[]
  containers: string[]
  maxHeight?: number
  maxBitrate?: number
}

/** The player's quality menu. */
export type Quality = 'auto' | 'original' | '1080' | '720' | '480'

export interface PlayRequest {
  caps: ClientCaps
  audioIndex?: number
  /** The id (Track.src) of the external track audioIndex names. */
  audioSrc?: string
  startSec?: number
  /** The quality menu's choice; online playback sends a source quality («1080p»). */
  quality?: Quality | (string & {})
  /** The HLS session this one replaces (seek, track or quality change). */
  replaceSession?: string
  /** Online: a re-play after a stale link, resolved fresh past the caches. */
  refresh?: boolean
}

/** What the quality menu means for this session. */
export interface QualityInfo {
  selected: Quality
  /** The cap in effect: «Авто» outside the home network is «Качество вне дома». */
  applied: Exclude<Quality, 'auto'>
  remote: boolean
}

/** PUT /api/hls/{session}: the player's heartbeat. */
export interface HlsStatus {
  segments: number
  running: boolean
  paused: boolean
}

export interface PlayResponse {
  mode: 'direct' | 'remux' | 'transcode' | (string & {})
  reason?: string
  url: string
  /** Same shape as MediaFile.subs: image-based tracks have burnIn and no url. */
  subtitles: SubTrack[]
  audio: Track[]
  audioIndex: number
  /** Where the stream really starts (a copied video starts on a keyframe). */
  startSec: number
  durationMs?: number
  /**
   * The file's recap, intro and credits (library files; [] when none are
   * known yet, absent from older servers and online streams).
   */
  segments?: PlaySegment[]
  quality?: QualityInfo | OnlineQualityInfo
  /** Online (mode 'online'): what the stream is. */
  kind?: 'hls' | 'mp4' | (string & {})
  /** Online: the proxy session (DELETE /api/online/s/{session} on close). */
  session?: string
}

/**
 * A part of a file the player offers to skip: seconds on the file timeline
 * (the clock of the subtitles). Credits ending more than 20 s before the
 * file does leave a scene after them.
 */
export interface PlaySegment {
  kind: 'recap' | 'intro' | 'credits' | (string & {})
  startSec: number
  endSec: number
}

/** The qualities of an online stream and the one playing. */
export interface OnlineQualityInfo {
  options: string[]
  current: string
}

// --- «Смотреть онлайн» (/api/online/*) ---

/** GET /api/online/sources: a working online source of a title. */
export interface OnlineSource {
  id: string
  name: string
  /** The quality hint of the source («1080p»), '' when unknown. */
  quality: string
  ref: string
}

/** A voice-over: the same level in that voice. */
export interface OnlineChoice {
  name: string
  active: boolean
  ref: string
}

export interface OnlineSeason {
  number: number
  name: string
  ref: string
}

/** A subtitle track of an online file or episode, as the picker shows it. */
export interface OnlineSubInfo {
  label: string
  lang: string
}

/** A playable episode; ref is a file ref (POST /api/online/play). */
export interface OnlineEpisode {
  season: number
  episode: number
  name: string
  ref: string
  qualities: string[]
  maxQuality?: string
  subtitles: number
  /** Its subtitles (label as the source gives it, language code or empty); a «call» entry knows them only when played. */
  subtitleTracks?: OnlineSubInfo[]
  unsupported?: boolean
}

/** A playable film entry (usually one per voice-over). */
export interface OnlineFile {
  name: string
  voice: string
  qualities: string[]
  maxQuality?: string
  ref: string
  subtitles: number
  subtitleTracks?: OnlineSubInfo[]
  unsupported?: boolean
}

export interface OnlineCandidate {
  title: string
  year?: number
  details?: string
  ref: string
}

/** GET /api/online/browse: one level of a source. */
export interface OnlineLevel {
  level: 'movie' | 'seasons' | 'episodes' | 'pick' | (string & {})
  voices?: OnlineChoice[]
  seasons?: OnlineSeason[]
  season?: number
  episodes?: OnlineEpisode[]
  files?: OnlineFile[]
  candidates?: OnlineCandidate[]
}

/** POST /api/online/play. */
export interface OnlinePlayRequest {
  ref: string
  /** A source quality («1080p»); '' = the server's default. */
  quality?: string
  startSec?: number
  caps: ClientCaps
  replaceSession?: string
  refresh?: boolean
}

/** An online watch position (GET /api/online/progress). */
export interface OnlineEntry {
  kind: 'movie' | 'series'
  tmdbId: number
  season: number
  episode: number
  positionMs: number
  durationMs: number
  finished: boolean
  source: string
  voice: string
  quality: string
  updatedAt: string
}

export interface OnlineLast {
  source: string
  voice: string
  quality: string
  season: number
  episode: number
}

export interface OnlineProgress {
  entries: OnlineEntry[]
  last: OnlineLast | null
}

/** GET /api/online/status (admin). */
export interface OnlineStatus {
  configured: boolean
  reachable: boolean
  sources: number
  version: string
}

/** The catalog «Найти и добавить» searches: TMDB, MusicBrainz, Open Library, or the query itself (manga). */
export type DiscoverKind = 'video' | 'music' | 'book' | 'audiobook' | 'comic'

export interface Candidate {
  tmdbId: number
  /** MusicBrainz release group or Open Library work (music, books). */
  extId?: string
  kind: 'movie' | 'series' | 'music' | 'book' | 'audiobook' | 'comic' | (string & {})
  title: string
  /** The artist (music) or the first author (books). */
  subtitle?: string
  year: number
  poster: string
  overview: string
  inLibrary: boolean
  libraryItemId: number | null
  /** Music: other names of the album (the MusicBrainz disambiguation, «Blue Album»). */
  aliases?: string[]
  /** Music found by a song title: the song of the query the album holds («Sad But True» on «Metallica»). */
  matchedTrack?: string
  /** Music of the artist the query names («metallica» → their albums, listed first): that artist. */
  byArtist?: string
  /** A kid's search result: «Попросить» (the server sends it to kid profiles only). */
  kidRequest?: KidRequestState
}

/** A kid's request («Попросить»): pending, approved (downloading), in_library once the library has it, or declined. */
export type KidRequestStatus = 'pending' | 'approved' | 'in_library' | 'declined'

export interface KidRequest {
  id: number
  /** Names the title ('tmdb:603', 'ext:…', 'name:…'): one request per kid and key. */
  key: string
  kind: Candidate['kind']
  tmdbId: number
  extId: string
  title: string
  /** The artist or the author. */
  subtitle: string
  year: number
  poster: string
  aliases: string[]
  rating: string
  status: KidRequestStatus
  /** Why it was declined ('': not said). */
  reason: string
  libraryItemId: number | null
  createdAt: string
  decidedAt?: string
  /** Who asked (the admins' list only). */
  user?: { id: number; name: string; avatar: string }
  /** A kid's own request of a title above its limit now (the limit was lowered): no title or cover. */
  hidden?: boolean
}

/** The kidRequest field of a kid's search result or title page. */
export interface KidRequestState {
  key: string
  /** May it be asked for (a search result's movie is checked when asked for). */
  allowed: boolean
  /** Why not, in the interface language. */
  blocked?: string
  request: KidRequest | null
}

export interface AcquireRequest {
  tmdbId: number
  kind: string
  /** Music and books: the catalog id, the album or book, its artist or author and year (the album's folder). */
  extId?: string
  title?: string
  artist?: string
  year?: number
  /** Music: other names of the album (Candidate.aliases). */
  aliases?: string[]
  season?: number
  episode?: number
  mode: 'stream' | 'download'
  releaseLink?: string
  /** Music: the name of the chosen release (evidence for the organize check). */
  releaseTitle?: string
  sourceId?: number
  force?: boolean
  /** releaseLink is a file of an OPDS catalog (Release.direct): downloaded as is. */
  direct?: boolean
  /** Music, a chosen release: the file indexes to download (the track step); absent = the whole release. */
  files?: number[]
  /** Another release chosen for the title: this download replaces the title's unfinished ones (movies and series). */
  replace?: boolean
  /** Music, the server's pick: «Скачать в другом формате» — a release of another format than «Формат загрузки» will do. */
  anyFormat?: boolean
  /** Books: the Open Library cover the candidate showed (the server caches it; any other host is ignored). */
  poster?: string
}

/** POST /api/acquire/files: the release whose file list the track step shows. */
export interface ReleaseFilesRequest {
  sourceId?: number
  releaseLink: string
  title: string
  artist: string
  year: number
  aliases: string[]
  direct?: boolean
  /** The song the person searched for: its track comes back `matched` («Выбрать треки…» checks only it). */
  song?: string
}

/** One file of a release; `index` is the release's own file index. */
export interface ReleaseFile {
  index: number
  path: string
  size: number
  /** A song, or anything else (cover, cue, log…). */
  kind: 'track' | 'extra'
  /** The album the file belongs to (the requested one is named as in the catalog). */
  album: string
  /** Checked by default (the requested album). */
  checked: boolean
  /** A track's disc and number from its file name (absent: unknown). */
  disc?: number
  no?: number
  /** The track is the searched song (ReleaseFilesRequest.song). */
  matched?: boolean
  /** In the download «Загрузки» has of this release (ReleaseFiles.inDownload). */
  downloading?: boolean
  /** The library's album has this song already. */
  inLibrary?: boolean
}

export interface ReleaseFiles {
  infoHash: string
  name: string
  /** The release is already on the server whole (or without its file list yet): no list, `message` says so. */
  added: boolean
  /** «Загрузки» has this release with some songs (`downloading`): the picked ones join that download. */
  inDownload?: boolean
  message?: string
  files: ReleaseFile[]
}

export interface AcquireResult {
  infoHash: string
  fileIndex: number
  streamUrl: string
  status: string
  /** Picked songs added to the release's download in «Загрузки» (or the album was downloading already). */
  existing?: boolean
  /** Where the songs go, when it is worth saying (localized). */
  note?: string
}

export type TorrentStatus = 'queued' | 'downloading' | 'seeding' | 'paused' | 'waiting_space' | 'error' | (string & {})

/**
 * Why a running download makes no headway (torrent.Status.Stalled): `no_peers`
 * — nobody to download from for a while (a magnet then has no metadata either);
 * `no_metadata` — a magnet has peers but still no metadata. Absent: nothing to say.
 */
export type TorrentStalled = 'no_peers' | 'no_metadata' | (string & {})

/**
 * What a short probe of the swarm found when the release was picked
 * (torrent.Status.Liveness): advisory only — a seeder may still connect in later.
 * `unseen`: no tracker and no DHT node knew a peer. Absent: never probed.
 */
export type TorrentLiveness = 'checking' | 'alive' | 'unseen' | (string & {})

/** torrent.Status.TrackerErrorKind: what a tracker's refusal means. */
export type TrackerErrorKind = 'bad_passkey' | 'not_registered' | 'rejected' | (string & {})

/** Why a download makes no headway, as far as the server knows (StalledNote). */
export interface TorrentHint {
  stalled?: TorrentStalled
  liveness?: TorrentLiveness
  /** The tracker's own words, passkeys hidden. */
  trackerError?: string
  trackerErrorKind?: TrackerErrorKind
}

/**
 * acquire.TorrentView.AutoReplace: `switched` — Antresol started this download
 * itself in place of a release nobody seeded; `none_alive` — no peer and no live
 * alternative was found.
 */
export type AutoReplaceNote = 'switched' | 'none_alive' | (string & {})

/** GET /api/torrent-engine (admin): can peers on the internet reach the server? */
export interface TorrentEngineStatus {
  listenPort: number
  /** The engine asks the router for the port by UPnP. */
  portForwarding: boolean
  /** Incoming peer handshakes since start. */
  incomingConns: number
  /** Downloads ran for a while and no peer ever connected in: the port is most likely closed. */
  incomingBlocked: boolean
}

/** GET /api/torrents (admin): torrent.Status plus the library side (acquire.TorrentView). */
export interface Torrent {
  infoHash: string
  name: string
  mode: 'stream' | 'download' | 'seed' | (string & {})
  status: TorrentStatus
  size: number
  done: number
  downRate: number
  upRate: number
  uploaded: number
  downloaded: number
  peers: number
  seeds: number
  ratio: number
  itemId: number | null
  /** RFC 3339. */
  addedAt: string
  completedAt?: string
  saveDir: string
  error?: string
  kind?: 'movie' | 'series' | (string & {})
  /** The TMDB title it was added for. */
  title?: string
  organized: boolean
  organizeError?: string
  /** All its files were gone at start: «Продолжить» downloads them again. */
  dataMissing?: boolean
  /** Profile id that added it. */
  addedBy?: number
  /** Profile name that added it (admin: «Добавил: …»). */
  addedByName?: string
  /** What organize did besides placing files, e.g. «Уже есть в медиатеке». */
  note?: string
  /** A direct download of an OPDS source (infoHash «direct-…»): status queued, downloading, done or error; no pause. */
  direct?: boolean
  /** Files chosen in the track step and files in the release (equal = the whole release). */
  selectedFiles?: number
  totalFiles?: number
  /** Music with a partial selection: the songs chosen and the release's songs (covers and CUE sheets left out). */
  selectedTracks?: number
  totalTracks?: number
  /** The cover, a file of the server's image cache (served by /api/images/{poster}); absent = none known. */
  poster?: string
  /** GET /api/torrents only: this profile finished the movie or every episode of the series (absent: other kinds, not in the library). */
  watched?: boolean
  stalled?: TorrentStalled
  /** See TorrentHint. */
  liveness?: TorrentLiveness
  trackerError?: string
  trackerErrorKind?: TrackerErrorKind
  /** The movie or series it was added for (and its season; absent = all or not a TMDB title). */
  tmdbId?: number
  season?: number
  autoReplace?: AutoReplaceNote
}

/** GET /api/torrents/{ih}/status: progress for a viewer waiting for «Смотреть сейчас». */
export interface StreamStatus {
  status: TorrentStatus
  mode: string
  size: number
  done: number
  downRate: number
  peers: number
  seeds: number
  /** Downloaded of needed bytes where the player reads next (?file=; 0 = unknown). */
  bufferHave?: number
  bufferWant?: number
  stalled?: TorrentStalled
}

/** POST /api/torrents/{ih}/promote. */
export interface PromoteResult {
  status: TorrentStatus
}

/** GET /api/discover/{kind}/{tmdbId}: a TMDB title that may not be in the library. */
export interface TitleInfo extends Candidate {
  originalTitle?: string
  backdrop: string
  genres: string[]
  runtime?: number
  rating?: string
  /** The IMDb id (see ItemDetail.imdbId). */
  imdbId?: string
  /** The leading actors (absent from an older server). */
  cast?: CastMember[]
  /** Series: regular seasons with their episode counts (empty when TMDB has none). */
  seasons: { number: number; episodes: number }[]
  /** «Следить» (see ItemDetail.follow). */
  follow?: FollowRef | null
}

/** A configured torrent source (indexer.Source; secrets are redacted). */
export interface Source {
  id: number
  definitionId: string
  name: string
  enabled: boolean
  settings: Record<string, string>
  status: 'ok' | 'error' | 'unknown' | (string & {})
  lastError?: string
  /** RFC 3339. */
  lastChecked?: string
  /** Lower = preferred. */
  priority: number
  /** This source is the backup way into that one (the same site signed in another way): searched only when it fails, shown inside it. */
  fallbackFor?: number
}

export interface CatalogOption {
  value: string
  label: string
}

/** One form field of a tracker definition. */
export interface CatalogField {
  name: string
  type: string
  label: string
  default?: string
  help?: string
  required: boolean
  advanced: boolean
  secret: boolean
  options?: CatalogOption[]
}

/** A tracker definition from the catalog (fields only in GET /api/sources/catalog/{id}). */
export interface CatalogEntry {
  id: string
  name: string
  description: string
  language: string
  type: 'public' | 'semi-private' | 'private' | (string & {})
  links: string[]
  custom: boolean
  fields?: CatalogField[]
}

export interface ImportedSource {
  name: string
  definitionId?: string
  source?: Source
  reason?: string
}

/** A found release (indexer.ScoredRelease). */
export interface Release {
  title: string
  details?: string
  link?: string
  magnet?: string
  infoHash?: string
  size: number
  seeders: number
  leechers: number
  publishDate?: string
  freeleech?: boolean
  sourceId: number
  sourceName: string
  resolution?: number
  source?: string
  codec?: string
  hdr?: boolean
  audio?: string[]
  /** Subtitle languages (ru, en, uk, ...) or ["sub"] when named without one; absent = none. */
  subs?: string[]
  /** Voice-over studios named by the release (LostFilm, Кубик в Кубе), in order of appearance. */
  studios?: string[]
  season?: number
  episode?: number
  episodeEnd?: number
  seasonPack?: boolean
  score: number
  excluded?: boolean
  reasons: string[]
  /** A file of an OPDS catalog, not a download. */
  direct?: boolean
  /** Books and audiobooks: the language (ru, en, ja); absent = unknown. */
  lang?: string
  /** Music: the format badge («FLAC», «MP3 320», «MP3 V0»); absent = not named. */
  audioFormat?: string
  /** Music: another format than «Формат загрузки» — listed last, never picked by itself. */
  formatMismatch?: boolean
}

/** GET /api/acquire/progress: aggregate download progress of one title (numbers only). */
export interface TitleProgress {
  status: 'active' | 'paused' | 'done'
  done: number
  size: number
  /** active only: every active download of the title is stalled. */
  stalled?: TorrentStalled
  /** active only: see TorrentHint. */
  liveness?: TorrentLiveness
  trackerError?: string
  trackerErrorKind?: TrackerErrorKind
}

export interface ReleaseSearch {
  releases: Release[]
  rejected: Release[]
  failed: { sourceId: number; name: string; message: string }[]
}

/** A line of the release stream: the sources that answered so far, and those still searched. */
export interface ReleaseProgress extends ReleaseSearch {
  pending: { sourceId: number; name: string }[]
}

/** GET/PUT /api/settings (admin). */
/** The language catalog (internal/lang). */
export type LangCode = 'ru' | 'en' | 'uk' | 'de' | 'fr' | 'es' | 'it' | 'pt' | 'pl' | 'ja' | 'zh' | 'ko'
export type Voice = 'dub' | 'original'

export interface Settings {
  quality: 720 | 1080 | 2160
  /** The first of `voices` (older clients). */
  voice: 'dub' | 'original'
  /** «Озвучка» in order: the Russian dub and/or the original. */
  voices?: Voice[]
  /** «Любимые озвучки»: voice-over studios, best first (absent on older servers). */
  studios?: string[]
  /** «Языки»: the viewer's languages, best first; every other language choice offers only these. */
  languages?: LangCode[]
  /** Languages of new books, audiobooks and comics/manga: a subset of `languages`, in its order. */
  bookLangs?: LangCode[]
  audiobookLangs?: LangCode[]
  comicLangs?: LangCode[]
  /** 0 = no limit. */
  maxSizeGB: number
  uploadLimited: boolean
  uploadLimitKBs: number
  streamCacheGB: number
  /** «Качество вне дома»: the cap of «Авто» outside the home network. */
  remoteQuality: 'original' | '1080' | '720' | '480'
  /** «Музыка вне дома»: the original, AAC 256 or AAC 128 кбит/с. */
  remoteMusic: 'lossless' | 'high' | 'low'
  /** New music: FLAC (true) or MP3 (legacy; musicFormat says more). */
  preferLossless: boolean
  /** «Формат загрузки»: FLAC, MP3 or the best of any. */
  musicFormat?: 'lossless' | 'mp3' | 'any'
  /** «Битрейт» of MP3: 320, V0, 256 and more, any. */
  mp3Bitrate?: '320' | 'v0' | '256' | 'any'
  /** «Превью при перемотке»: the server makes scrub previews in the background (on by default). */
  trickplay?: boolean
  /** «Место для превью», GB (10 by default; the least recently viewed go first). */
  trickplayBudgetGB?: number
  /** Look for missing subtitles on OpenSubtitles. */
  autoSubtitles: boolean
  /** At least one of `languages`, in its order. */
  subtitleLangs: LangCode[]
  /** An OpenSubtitles key is set on the server (response only). */
  subtitlesAvailable: boolean
  /** «Смотреть онлайн» (matters only when the server has online sources configured). */
  onlineEnabled?: boolean
  /** «Язык описаний медиатеки»: the language of stored TMDB metadata, applied to new scans (absent on older servers). */
  metadataLanguage?: 'ru' | 'en'
  /** «Отправка на Kindle»: the SMTP account books leave from. An answer never carries the password. */
  mail?: MailSettings
  /** A mail password is stored (response only). */
  mailPasswordSet?: boolean
}

export interface MailSettings {
  host: string
  port: number
  username: string
  /** Only in a request; empty or absent keeps the stored one. */
  password?: string
  from: string
}

/** GET /api/stats (admin). */
export interface Stats {
  disk: { total: number; used: number; free: number; categories: { id: string; bytes: number }[] }
  counts: { movies: number; series: number; episodes: number; albums: number; tracks: number; books: number; audiobooks: number; comics: number }
  watchedByUser: { user: string; avatar: string; hours30d: number }[]
  topItems30d: { id: number; kind: CardKind; title: string; year?: number; poster: string; viewers: number; hours: number }[]
  // the sources build's (none from the core build)
  seeding?: { count: number; uploadedTotal: number; ratio: number }
}

/** A title in GET /api/stats/storage (a movie, series, album, book, audiobook or comic), with what to trash to delete it. */
export interface StorageItem {
  id: number
  kind: CardKind
  title: string
  /** an album's artist */
  artist?: string
  year?: number
  poster: string
  size: number
  files: number
  addedAt: string
  lastWatched: string | null
  trash: { root: string; paths: string[] }[]
}

/**
 * GET /api/stats/disk/{category} (sources build, admin): what a disk category
 * is made of, largest first. Entries are the first-level folders of the
 * category (for «other», folders below the media disk's root); a file
 * linked into the library counts in the library, so «downloads» lists the
 * download data that is not in it.
 */
export interface DiskBreakdown {
  category: string
  /** «other» is still being walked: ask again. */
  pending: boolean
  /** The category's total. */
  bytes: number
  entries: { name: string; bytes: number; dir: boolean }[]
  /** The entries past the first 100, summed. */
  more?: { count: number; bytes: number }
  /** «other» only: what no folder explains (system files, the file system). */
  rest?: number
  /** «other» only: the walk ran out of time; the entries are what it counted. */
  partial?: boolean
}

/** Every title with files on the disk, largest first; lastWatched: by anyone. */
export interface StorageReport {
  items: StorageItem[]
}

/** GET /api/library/status: the scan job (library.JobStatus). */
export interface LibraryStatus {
  running: boolean
  queued: boolean
  startedAt?: string
  finishedAt?: string
  error?: string
  movies: number
  episodes: number
  albums: number
  tracks: number
  books: number
  audiobooks: number
  comics: number // comic volumes
  files: number
  skipped: number
  removed: number
}

export interface DiskUsage {
  used: number
  total: number
  level: 'ok' | 'warn' | 'full'
  /** Why the media disk is unavailable (downloads are held). */
  offline?: string
}

/** Server-sent event from GET /api/events. */
export interface ServerEvent<T = unknown> {
  type: 'torrent.progress' | 'library.updated' | 'disk.warning' | 'source.status' | (string & {})
  data: T
}

// --- Files (Finder-style browser, /api/files/*) ---

export type FileKind =
  | 'folder'
  | 'video'
  | 'audio'
  | 'image'
  | 'subtitle'
  | 'text'
  | 'book'
  | 'comic'
  | 'archive'
  | 'torrent'
  | 'other'
  | (string & {})

/** A section of the media disk (Фильмы, Загрузки, Весь диск …). */
export interface FileRoot {
  id: string
  title: string
  icon: string
}

export interface FileItemRef {
  id: number
  kind: CardKind
  title: string
}

export interface FileTorrentRef {
  infohash: string
  seeding: boolean
}

/** One file or folder. `path` is relative to the root. */
export interface FileEntry {
  name: string
  path: string
  isDir: boolean
  size: number
  /** RFC 3339. */
  mtime: string
  kind: FileKind
  childCount?: number
  thumb?: string
  item?: FileItemRef
  torrent?: FileTorrentRef
}

export interface FileCrumb {
  name: string
  path: string
}

export interface FileListing {
  root: string
  path: string
  breadcrumbs: FileCrumb[]
  entries: FileEntry[]
  /** A search hit the result limit or the time limit. */
  truncated?: boolean
}

export interface FileProbe {
  container: string
  video?: { codec: string; width: number; height: number; hdr: boolean }
  audio: { codec: string; lang: string; title: string; channels: number }[]
  subs: { codec: string; lang: string; title: string; forced: boolean }[]
  /** Seconds. */
  duration: number
  durationMs: number
}

export interface FileStat extends FileEntry {
  dirSize?: number
  /** The folder size is still being counted: ask again shortly. */
  dirSizePending?: boolean
  probe?: FileProbe
}

export interface FileUsage {
  total: number
  used: number
  free: number
  categories: { id: string; bytes: number }[]
}

export interface TrashItem {
  id: number
  root: string
  /** Original path, relative to root. */
  path: string
  name: string
  isDir: boolean
  size: number
  deletedAt: string
}

export interface TrashResult {
  trashed: TrashItem[]
  warnings: string[]
  /** Something is being shared by a managed download: repeat with confirm. */
  needsConfirm: boolean
  error?: string
}

// --- Music ---

/** A track in albums, lists and playlists (Go TrackDTO). */
export interface Song {
  id: number
  title: string
  artist: string
  artistId: number
  albumId: number
  album: string
  discNo: number
  trackNo: number
  durationMs: number
  fileId: number
  lossless: boolean
  hasLyrics: boolean
  cover: string
  starred: boolean
  /** A slice of a CUE image: its file is the whole disc. */
  cue?: boolean
  /** ReplayGain («Выравнивание громкости»): dB on -18 LUFS and linear peaks; null while unknown. */
  gainTrackDb?: number | null
  gainAlbumDb?: number | null
  peakTrack?: number | null
  peakAlbum?: number | null
}

/** What «Перемешать» draws from (GET /api/music/shuffle). */
export type ShuffleSource = { kind: 'all' } | { kind: 'genre'; genre: string } | { kind: 'artist'; artistId: number }

/** A genre of visible albums (Go GenreDTO). */
export interface Genre {
  name: string
  albums: number
  songs: number
}

export interface ArtistRef {
  id: number
  name: string
}

export interface AlbumDetail {
  card: Card
  artist: ArtistRef
  year: number
  genres: string[]
  durationMs: number
  trackCount: number
  /** «Lossless · FLAC», «MP3 320 кбит/с». */
  quality: string
  tracks: Song[]
  starred: boolean
}

export interface MusicHome {
  shelves: Shelf[]
}

export type AlbumSort = 'added' | 'title' | 'artist' | 'year' | 'random' | 'frequent' | 'recent'

export interface SongsPage {
  items: Song[]
  total: number
}

export interface ArtistDetail {
  card: Card
  albums: Card[]
  /** Up to 10, by plays. */
  topTracks: Song[]
  appearsOn: Card[]
}

/** A playlist summary (Go PlaylistDTO). */
export interface Playlist {
  id: number
  name: string
  trackCount: number
  durationMs: number
  cover: string
  /** Up to 4 distinct album covers, in track order. */
  covers: string[]
  updatedAt: string
}

/** A playlist entry; `idx` is its stored position (what PATCH remove/order take). */
export interface PlaylistTrack extends Song {
  idx: number
}

export interface PlaylistDetail {
  playlist: Playlist
  tracks: PlaylistTrack[]
}

export interface PlaylistPatch {
  name?: string
  /** Track ids to add at the end. */
  append?: number[]
  /** Stored positions (`PlaylistTrack.idx`) to drop. */
  remove?: number[]
  /** Stored positions of the visible entries in their new order. */
  order?: number[]
}

export interface Lyrics {
  lyrics: string
  /** LRC with [mm:ss.xx] time tags. */
  synced: boolean
  /**
   * The track's correction of the synced words, ms added to every line time
   * (positive: later), on top of the LRC's [offset:]. Older servers omit it.
   */
  offsetMs?: number
}

// --- Books ---

export type BookKind = 'book' | 'audiobook' | 'comic'
export type BookSort = 'added' | 'title' | 'author'

export interface BooksHome {
  /** reading, want, recent, audiobooks, comics, done — empty ones are omitted. */
  shelves: Shelf[]
}

export interface BooksPage {
  items: Card[]
  total: number
}

export interface BookSeries {
  name: string
  index: number
}

export interface BookFile {
  id: number
  /** EPUB, FB2 (also .fb2.zip), MOBI, AZW3, PDF, MP3, M4B, CBZ… */
  format: string
  size: number
  durationMs: number
}

export interface ReadingProgress {
  /** 0..1 */
  fraction: number
  /** EPUB CFI, or `page:N` for PDF and comics. */
  locator: string
  positionMs: number
  durationMs: number
  finished: boolean
}

export interface BookChapter {
  idx: number
  title: string
  fileId: number
  startMs: number
  endMs: number
}

export interface VolumeCard extends Card {
  pages: number
  direction: 'rtl' | 'ltr' | (string & {})
  fileId: number
}

/** A book, audiobook, comic series or comic volume page. */
export interface BookDetail {
  card: Card
  kind: 'book' | 'audiobook' | 'comic' | 'comic_volume'
  authors: string[]
  narrator: string
  series: BookSeries | null
  description: string
  year: number
  /** Language code from the file (ru, en…), may be empty. */
  lang: string
  files: BookFile[]
  seriesId: number
  nextId: number
  /** The file the reader opens (EPUB > FB2 > MOBI > AZW3 > PDF); 0 if none. */
  readFileId: number
  /** «Отправить на Kindle» works for this profile and book. */
  canSendToKindle?: boolean
  /** This profile has sent the book to its present Kindle address. */
  sentToKindle?: boolean
  progress: ReadingProgress | null
  /** On the «Хочу прочитать» list. */
  want: boolean
  durationMs: number
  chapters: BookChapter[]
  volumes: VolumeCard[]
  direction: string
}

/** A bookmark, highlight or note (GET/PUT /api/annotations/{itemId}). */
export type AnnotationKind = 'bookmark' | 'highlight' | 'note'
/** The four highlight colours (a bookmark has none). Defined in the MPL
 * reader (an MPL file must not import AGPL types), re-exported here. */
export type { AnnotationColor } from '../reader/highlights'
export interface Annotation {
  /** Made by the client (the offline queue sends it again safely). */
  id: string
  kind: AnnotationKind
  /** The reading-progress locator: a CFI point (bookmark) or range (highlight, note), `page:N` (PDF), `ms:N` (audiobook). */
  locator: string
  /** 0..1 through the book: the order of the list. */
  fraction: number
  /** Where it is: the chapter, «Страница 12». */
  label: string
  /** The highlighted text (at most 1000 characters). */
  excerpt: string
  note: string
  color: AnnotationColor | ''
  createdAt: string
  /** When the server last took a change (in a write: this device's time, a hint only). */
  updatedAt: string
  /** The server's revision of it (0: not on the server yet); a write sends the one it was made on. */
  rev: number
  /** A tombstone: deleted, kept so an older write cannot bring it back. */
  deleted: boolean
}

/** GET /api/comics/{fileId}/pages. */
export interface ComicPages {
  count: number
  direction: 'rtl' | 'ltr' | (string & {})
  /** [width, height] of each page from its header; [0, 0] when unknown. */
  sizes?: [number, number][]
}

// --- «Переезд на Vault» (/api/migrate*, admin) ---

/** The media servers every build migrates from; a build may add its own sources. */
export type MigrateSourceId = 'jellyfin' | 'kavita' | 'audiobookshelf' | (string & {})

/** One old service: found under /import or not, and what it has. */
export interface MigrateSource {
  id: MigrateSourceId
  name: string
  found: boolean
  path: string
  /** «3 пользователя» / «Не найден — смонтируйте /import/…». */
  detail: string
  /** A problem reading it ('' = none). */
  error: string
}

export type MigrateStatusKind = 'planned' | 'imported' | 'skipped' | 'failed' | 'warning'

export interface MigrateLine {
  source: string
  kind: 'torrent' | 'source' | 'follow' | 'progress' | 'user' | 'leftover' | (string & {})
  name: string
  status: MigrateStatusKind
  reason: string
  ref: string
  size: number
  prev?: string
}

/** A user of Jellyfin, Kavita or Audiobookshelf and the Vault profile proposed for them. */
export interface MigrateUser {
  source: string
  sourceId: string
  sourceName: string
  vaultUserId: number | null
  rows: number
  /** Set on a row standing for a whole service whose database cannot be read. */
  error?: string
}

export interface MigrateReport {
  id: string
  sources: string[]
  dryRun: boolean
  planId: string
  planHash: string
  rollbackOf?: string
  startedAt: string
  /** Zero time ('0001-01-01T00:00:00Z') while running or when interrupted. */
  finishedAt: string
  lines: MigrateLine[] | null
  counts: Partial<Record<MigrateStatusKind, number>> | null
  /** History only: the downloads this run moved (a build's own source). */
  torrents?: number
}

/** What changes during a run or a scan (GET /api/migrate/state reads no old database). */
export interface MigrateState {
  running: string | null
  /** Only on a server with the sources build's old services (not the core build or VAULT_LIBRARY_ONLY). */
  state?: { switchedAt: string | null }
  /** Same: absent where there is nothing to switch. */
  pendingFollows?: number
  libraryReady: { ok: boolean; reason: string }
}

export interface MigrateStatus extends MigrateState {
  sources: MigrateSource[]
  leftovers: MigrateLine[]
  users: MigrateUser[]
  profiles: { id: number; name: string }[]
}

export interface MigrateRunRequest {
  sources: MigrateSourceId[]
  dryRun: boolean
  planId?: string
  /** "<source>:<sourceUserId>" → Vault profile id; a missing key = «Не переносить». */
  userMap: Record<string, number>
}

export interface MigrateSwitchPlan {
  follows: number
  episodes: number
  movies: number
  errors: number
  /** Why nothing can be counted now (full disk, …): the switch waits; '' = none. */
  blocked: string
}

// «Импорт медиатеки»

export type ImportServiceId = 'spotify' | 'apple' | 'yandex' | 'youtube' | 'deezer' | 'file'

/** A tile of «Импорт»: how it connects, the owner's setup steps while not configured, the files it can also be read from. */
export interface ImportService {
  id: ImportServiceId
  configured: boolean
  connect: 'redirect' | 'paste' | 'device' | 'file' | 'app'
  /** Russian steps; empty when configured. */
  setup: string[]
  files: string[]
}

/** «Подключить»: the service's sign-in page and how the browser comes back (to this server, or a loopback address to paste). */
export interface ImportConnect {
  authorizeUrl: string
  mode: 'redirect' | 'paste'
  redirectUri: string
}

/** «Подключить» of a device sign-in (Yandex): «Откройте <verificationUrl> и введите код <userCode>»; poll with pollId every interval seconds. */
export interface ImportDevice {
  pollId: string
  userCode: string
  verificationUrl: string
  /** Seconds the code is valid. */
  expiresIn: number
  /** Seconds between polls. */
  interval: number
}

export type ImportJobStatus = 'reading' | 'preview' | 'matching' | 'downloading' | 'done' | 'failed' | 'cancelled'

export interface ImportPlaylist {
  id: number
  name: string
  songs: number
  /** false: the service would not give its songs (a followed Spotify playlist); unticked and locked. */
  readable: boolean
  selected: boolean
}

/** The selected songs by state (total includes the ones not matched yet). */
export interface ImportProgress {
  total: number
  matched: number
  review: number
  notFound: number
  have: number
  queued: number
  downloading: number
  done: number
  failed: number
  skipped: number
}

/** An import job (the server's JobDTO). */
export interface ImportJob {
  id: number
  service: ImportServiceId | (string & {})
  status: ImportJobStatus
  createdAt: string
  counts: { songs: number; liked: number; albums: number; playlists: number; savedAlbums: number }
  estimatedBytes: number
  /** Zeros when unknown. */
  disk: { free: number; total: number }
  favorites: boolean
  playlists: ImportPlaylist[]
  progress: ImportProgress
  error: string
  report: { savedAlbumsMissing: number; unresolved: number }
  /** Matching waits for MusicBrainz until then (an outage). */
  matchPausedUntil?: string
}

export interface ImportJobPatch {
  favorites?: boolean
  /** Import playlist id → ticked. */
  playlists?: Record<string, boolean>
}

export type ImportGroupState =
  | 'waiting'
  | 'searching'
  | 'waiting_space'
  | 'waiting_download'
  | 'downloading'
  | 'done'
  | 'have'
  | 'not_found'
  | 'no_format'
  | 'failed'

/** An album the import fetches (or found in the library). */
export interface ImportGroup {
  id: number
  artist: string
  album: string
  year: number
  songs: number
  state: ImportGroupState
  bytes: number
  /** Bytes downloaded so far. */
  done: number
  error: string
  infoHash: string
  /** waiting_download: the download the album waits for, as «Загрузки» names it (another import's or «На сервер»). */
  waitingFor?: string
}

/** A song waiting for the admin's decision. */
export interface ReviewTrack {
  id: number
  artist: string
  title: string
  album: string
  /** Russian: «Нет точного совпадения», «Несколько исполнителей с таким именем», «Не найдено в MusicBrainz». */
  reason: string
  candidates: Candidate[]
}

/** The album chosen for a song, or {skip: true}. */
export interface ImportResolve {
  extId?: string
  title?: string
  artist?: string
  year?: number
  aliases?: string[]
  skip?: boolean
}

/** import.progress (admins). */
export interface ImportProgressEvent {
  jobId: number
  status: ImportJobStatus
  progress: ImportProgress
}

/** A song of an imported playlist that has not arrived yet (position: its place in the service's playlist, 0-based). */
export interface PendingEntry {
  position: number
  artist: string
  title: string
  state: string
  /** state waiting_download: the download its album waits for. */
  waitingFor?: string
}

/**
 * GET /api/items/{id}/family: another profile's place in an item. `itemId`
 * is where the position is (the movie, book, audiobook, an episode of a
 * series or a volume of a comic). For a series or comic, done/total count
 * its finished episodes or volumes, `next` says itemId is the one after the
 * last finished (not started yet) and `finished` that everything is done.
 */
export interface FamilyMember {
  userId: number
  name: string
  avatar: AvatarId
  isKid: boolean
  itemId: number
  positionMs: number
  durationMs: number
  /** 0..1 of itemId */
  fraction: number
  finished: boolean
  locator?: string
  /** RFC 3339 */
  updatedAt: string
  season?: number
  episode?: number
  volume?: number
  /** The episode's or volume's title. */
  title?: string
  /** A `page:N` position: N, and the page count when known. */
  page?: number
  pages?: number
  next?: boolean
  done?: number
  total?: number
}

/**
 * GET /api/items/{id}/related: the «Похожие» and «Коллекция» rows of a movie's
 * or series' page. The library's cards come first; in the sources build an adult
 * also gets a few TMDB titles the library lacks (id 0, onServer false,
 * tmdbKind/tmdbId: their /title page). Empty: similar [] and collection null.
 */
export interface RelatedRows {
  similar: Card[]
  collection: CollectionRow | null
}

/** A movie's TMDB collection (a franchise), its parts in release order (GET /api/items/{id}/collection). */
export interface CollectionRow {
  id: number
  /** Without TMDB's «collection» word: «Матрица». */
  name: string
  overview?: string
  /** Image URLs. */
  poster?: string
  backdrop?: string
  parts: Card[]
}

/**
 * GET /api/files/{id}/trickplay: the scrub previews of a file. Frame i
 * shows [i·interval, (i+1)·interval) s and sits in sheet
 * ⌊i / (columns·rows)⌋ at column i % columns, row ⌊i / columns⌋ % rows;
 * every sheet is columns·width × rows·height px.
 */
export interface TrickplayIndex {
  version: number
  key: string
  interval: number
  width: number
  height: number
  columns: number
  rows: number
  count: number
  sheets: number
}

// --- «Экранное время» (/api/me/screen-time, /api/users/:id/screen-time) ---

/** An allowed time of day, server time ("08:00"–"20:30"; to < from crosses midnight). */
export interface ScreenWindow {
  from: string
  to: string
}

/** A kid profile's rules; null is no limit (audioMin null: music and audiobooks are never limited). */
export interface ScreenLimits {
  videoMin: number | null
  audioMin: number | null
  /** The window of a screen day that starts on a weekday / at a weekend (it may run past midnight, not past the day start). */
  weekday: ScreenWindow | null
  weekend: ScreenWindow | null
  /** When the screen day starts, "04:00" by default: usage and the bonus reset then. */
  dayStart: string
}

export interface ScreenCategoryStatus {
  /** The day's limit without the bonus (null: none). */
  limitMin: number | null
  bonusMin: number
  usedSec: number
  /** How long playback may go on now (null: nothing ends it today). */
  remainingSec: number | null
  allowed: boolean
  reason?: 'limit' | 'hours'
  /** What the kid reads, in the interface language. */
  message?: string
}

export interface ScreenStatus {
  limited: boolean
  day: string
  hours: ScreenWindow | null
  video: ScreenCategoryStatus
  /** null: music and audiobooks are not limited. */
  audio: ScreenCategoryStatus | null
  /** Some rule limits the category: nothing of it downloads or goes to the device. */
  videoLimited: boolean
  audioLimited: boolean
}

export interface ScreenTitle {
  itemId: number
  category: 'video' | 'audio'
  seconds: number
  title: string
  kind?: string
  poster?: string
}

export interface ScreenDay {
  day: string
  videoSec: number
  audioSec: number
}

export interface ScreenReport {
  limits: ScreenLimits
  status: ScreenStatus
  /** Today, most first (an episode's time is its series'). */
  titles: ScreenTitle[]
  /** The last 7 days, today last. */
  days: ScreenDay[]
  /** Other profiles the kid can switch to without a password and play unlimited. */
  passwordless: string[]
}

/** GET /api/screen-time/limited (admin): kids with rules, and the profiles open without a password. */
export interface ScreenLimited {
  userIds: number[]
  passwordless: { id: number; name: string }[]
}

// --- tracking: marks, «Вышло новое», calendar (internal/api/tracking_handlers.go) ---

export type MarkState = 'planned' | 'watching' | 'completed' | 'on_hold' | 'dropped'

/** The profile's mark on a title. */
export interface TitleMark {
  state: MarkState
  score: number | null
  origin: string
  updatedAt: string
}

/** A tracked title; itemId is its library item (absent: not in the library). */
export interface TrackTitle {
  kind: 'series' | 'movie'
  tmdbId: number
  name: string
  poster: string
  status: string
  itemId?: number
}

/** An episode (or a movie's release) of a tracked title. */
export interface TrackingItem {
  title: TrackTitle
  season: number
  episode: number
  name: string
  airDate?: string
  /** When it became available (null: not yet). */
  firstSeen: string | null
  kinds: string[]
}

export interface CalendarDay {
  date: string
  items: TrackingItem[]
}
