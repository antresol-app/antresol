// API contracts shared by the whole web app. Field names mirror the Go JSON tags.

import type { AnnotationColor } from '../reader/highlights'

export type AvatarId = `grad-${1 | 2 | 3 | 4 | 5 | 6 | 7 | 8}` | (string & {})

export interface Profile {
  id: number
  name: string
  avatar: AvatarId
  /** The profile's photo (its address, versioned; absent: none): drawn in place of the letter on the coloured circle. */
  photo?: string
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
  /** A kid profile may ask for titles here («Попросить»). */
  canRequest?: boolean
  /** An admin has kids' requests to answer here («Управление → Запросы»). */
  requests?: boolean
  /** The server serves its library only (VAULT_LIBRARY_ONLY): no «Найти и
   * добавить», «Следить», downloads, sources, import or migration. */
  libraryOnly?: boolean
  /** The server runs addons (not VAULT_ADDONS=off): «Управление → Расширения». */
  addons?: boolean
  /** «Общая медиатека» (all) or «Моя медиатека» (mine): the library this
   * profile's pages show (always all for a kid; older servers send none). */
  libraryScope?: LibraryScope
  /** /api/me: the managed account this device writes its history into
   * («Отдельная история на этом устройстве»); null or absent: the profile's own. */
  deviceHistory?: DeviceHistory | null
}

/** A device's separate history: the managed account it writes into. */
export interface DeviceHistory {
  id: number
  name: string
}

/** A managed account: a history of its own under a profile (a shared TV, «Гостиная»). */
export interface ManagedAccount {
  id: number
  name: string
  avatar: string
  /** The devices writing into it now. */
  devices: { id: number; kind?: 'native' | 'web'; name: string; model: string }[]
  /** The admin's view only. */
  owner?: { id: number; name: string }
}

/** Which library a page lists: the shared one, or the titles this profile brought. */
export type LibraryScope = 'all' | 'mine'

export interface SetupState {
  needsSetup: boolean
  /** Outside the home network: no first-run setup and no passwordless profiles. */
  remote?: boolean
}

/** GET /api/about: what runs on the server and where its source is (AGPL §13). */
/** GET /api/updates (admin): the server's version against its latest public release. */
export interface CoreUpdate {
  version: string
  commit: string
  /** The latest release's tag («v0.9.0»); '' when the check failed (`checked` false). */
  latest: string
  releaseUrl?: string
  checked: boolean
  /** `latest` is newer than `version` (never for a build without a version). */
  newer: boolean
  /** The server runs in a container: it is updated with Compose, from outside. */
  docker: boolean
}

/** GET /api/about/local: the server's name on the home network (mDNS). */
export interface LocalName {
  /** off: mDNS off; probing: checking the name is free; ready: url works at home;
   * bridge: a Docker bridge network, multicast does not leave the container, and no mDNS announcer beside it;
   * vm: the host network is a VM's (Docker Desktop); failed: no socket or no free name. */
  state: 'off' | 'probing' | 'ready' | 'bridge' | 'vm' | 'failed'
  /** antresol.local (antresol-2.local when another server holds it); only when ready. */
  name?: string
  /** http://antresol.local:8420; only when ready. */
  url?: string
}

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
/** «Ключи API и ИИ»: what a personal API key may do (GET /api/me/api-keys). */
export type ApiKeyScope = 'read' | 'act' | 'admin'

/** A personal API key as the list shows it (never the key itself). */
export interface ApiKey {
  id: number
  name: string
  scope: ApiKeyScope
  /** The key's last four characters. */
  hint: string
  createdAt: string
  lastUsedAt: string | null
  lastUsedFrom?: string
}

export interface ApiKeysState {
  keys: ApiKey[]
  /** The profile is an admin: it may make keys with administration. */
  canAdmin: boolean
  /** The server's public address (VAULT_PUBLIC_URL), "" when not set. */
  publicUrl: string
  mcpPath: string
}

/** A row of the keys' log: a change made with a key, or a refusal. */
export interface ApiKeyEvent {
  at: string
  keyName: string
  method: string
  path: string
  status: number
  /** The MCP tool it was made for ("mcp:set_watched"). */
  via?: string
  client?: string
}

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
  userPhoto?: string
  /** Its separate history (null or absent: the profile's own). */
  history?: DeviceHistory | null
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
  /** This profile finished the movie or episode, or every episode of the series or season. */
  watched?: boolean
  /** Episodes: their own 16:9 picture (the stored still, else /api/images/frame/{id}). */
  thumb?: string
  /** Facts worth a chip for the kind: FLAC 24/96, 4K, RU, EPUB. */
  badges?: string[]
  /** Episodes and seasons: the parent series. */
  seriesId?: number
  tmdbKind?: 'movie' | 'series'
  tmdbId?: number
  /** A series title card: the episode it names (missing = 0). */
  season?: number
  episode?: number
  resumeSec?: number
  /** «Продолжить» from an addon (home.continue, /api/home?addons=1; id is 0): the addon that sent it. */
  addonId?: string
  /** An addon card's key, namespaced by the core («addon:<id>:<key>»). */
  key?: string
  /** The app page an addon card opens («/…»); without it the card opens its TMDB title page. */
  href?: string
  /**
   * Sources build: a title on its way into the library (GET /api/library/pending).
   * With id 0 the card is the download's own (tinted, opened by tmdbKind/tmdbId);
   * otherwise the progress sits on the library's card of that id.
   */
  pending?: PendingState
  /** «Вышло новое»: why the profile follows the title (its mark and the list that set it, or its watchlist). */
  why?: FollowWhy
}

/**
 * Why a profile follows a tracked title: a live mark (state, and origin, the list that set it:
 * antresol | shikimori | myshows), or `watchlist` for a library title on its watchlist with no mark.
 */
export interface FollowWhy {
  state: 'planned' | 'watching' | 'on_hold' | 'watchlist'
  origin?: string
}

/** What brings a pending card's title into the library (an addon's download, D3). */
export interface PendingState {
  /** The addon's own key, namespaced by the core («<addon>:<key>»). */
  key: string
  /** `error` reaches admins only. */
  status: 'active' | 'waiting' | 'paused' | 'organizing' | 'error'
  done: number
  size: number
  /** A line under the progress, already in the profile's language (why it makes no headway). */
  hint?: string
  /** The app page the card opens (checked by the core); absent: the card is no link. */
  href?: string
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
  /** «Пропускать сам»: the profile's, on every device (an older server: absent). */
  autoSkip?: AutoSkipPrefs
}

/** What a profile skips without pressing the button. */
export interface AutoSkipPrefs {
  recap: boolean
  intro: boolean
  credits: boolean
}

/** «Уведомления на почту»: the core's topics a profile may hear about (the addons' are addonTopics). */
export type NotifyTopic = 'episodes' | 'available' | 'requests' | (string & {})

/** A profile's choice for one addon topic (NotifyPrefs.addonTopics): its title in the profile's language. */
export interface AddonTopicPref {
  addon: string
  topic: string
  title: string
  on: boolean
}

/** A profile's notification settings (GET /api/me/notify, an admin's /api/users/{id}/notify). */
export interface NotifyPrefs {
  email: string
  /** The address was confirmed by its verification letter (code or link). */
  verified: boolean
  episodes: boolean
  available: boolean
  requests: boolean
  /** Letters wait from 22:00 to 08:00 (server time). */
  quietHours: boolean
  /** The letters' language: ru | en; '' the server's. */
  lang: string
  /** The topics this profile may turn on. */
  topics: NotifyTopic[]
  /** The addons' topics this profile may turn on, shown after the core's (none: absent). */
  addonTopics?: AddonTopicPref[]
  /** The server has a mail account. */
  mailReady: boolean
  /** Letters carry links (the server knows its public address). */
  publicLinks: boolean
  /** The mail server refused the address for good: unconfirmed until confirmed again. */
  bounced: boolean
  /** The channels the profile chose (both on until it turns one off): mail needs an address, Telegram a linked chat. */
  emailOn?: boolean
  telegramOn?: boolean
  /** The server has a Telegram bot, so a chat can be linked; the bot's name without «@». */
  telegramReady?: boolean
  telegramBot?: string
  /** The profile's chat is linked with the bot; what to call it («@name»). */
  telegramLinked?: boolean
  telegramName?: string
}

/** «Подключить Telegram»: a one-time code and the link that carries it (POST /api/me/notify/telegram/link). */
export interface TelegramLink {
  code: string
  url: string
  bot: string
  /** RFC 3339: until when the code works. */
  expires: string
}

/** The server's Telegram bot (admin; the token is write-only). */
export interface TelegramBot {
  set: boolean
  username?: string
  name?: string
  /** The server is asking Telegram for the bot's messages. */
  polling: boolean
  /** What is in the way of that. */
  problem?: 'unauthorized' | 'conflict' | 'unreachable'
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
  /** Movies and series: the TMDB id (a series' seasons that are not on the server yet). */
  tmdbId?: number
  /** Movies and series: the IMDb id ("tt0816692"), the key of GET /api/ratings/{imdbId}. */
  imdbId?: string
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
  /** The quality menu's choice; a stream sends a source quality («1080p»). */
  quality?: Quality | (string & {})
  /** The HLS session this one replaces (seek, track or quality change). */
  replaceSession?: string
  /** A stream: a re-play after a stale link, resolved fresh past the caches. */
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

/**
 * GET /api/play/input/{id}: a video an addon serves, registered for this
 * profile (/play/input/:id). `itemId` 0: no library item; `startSec` is
 * the profile's saved position of that item.
 */
export interface PlayerInput {
  id: string
  addonId: string
  /** The file's place in the addon's collection («Show/Show.S01E01.mkv»). */
  name: string
  itemId: number
  title?: { kind: 'movie' | 'series'; tmdbId: number; season: number; episode: number }
  /** A slow source: a slow start or a stall is the source, not the device. */
  slow: boolean
  /** The addon's own data, as it registered it. */
  meta?: unknown
  startSec: number
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
   * known yet, absent from older servers and from an addon's streams).
   */
  segments?: PlaySegment[]
  quality?: QualityInfo | StreamQualityInfo
  /** A stream (mode 'stream'): what it is. */
  kind?: 'hls' | 'mp4' | (string & {})
  /** A stream: its proxy session (the player's `stream.stop` closes it). */
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

/** The qualities of a stream's source and the one playing. */
export interface StreamQualityInfo {
  options: string[]
  current: string
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
  user?: { id: number; name: string; avatar: string; photo?: string }
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

/** What a kid may ask for (GET /api/requests/search): a TMDB movie or series, or an album, book, audiobook or manga of an addon's catalog. */
export interface RequestCandidate {
  kind: 'movie' | 'series' | 'music' | 'book' | 'audiobook' | 'comic' | (string & {})
  /** Movies and series. */
  tmdbId: number
  /** The catalog's id (albums, books). */
  extId: string
  title: string
  /** The artist or the author ('' for manga). */
  subtitle: string
  year: number
  /** Movies and series only: albums and books come without a cover. */
  poster: string
  explicit: boolean
  aliases: string[]
}

/** A kid's request search result: the candidate with its kidRequest. */
export interface KidCandidate extends RequestCandidate {
  kidRequest: KidRequestState | null
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

/** GET /api/title/{kind}/{tmdbId}: a TMDB title that may not be in the library (the core title page). */
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
  /** «Качество вне дома»: the cap of «Авто» outside the home network. */
  remoteQuality: 'original' | '1080' | '720' | '480'
  /** «Музыка вне дома»: the original, AAC 256 or AAC 128 кбит/с. */
  remoteMusic: 'lossless' | 'high' | 'low'
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
  /** «Язык описаний медиатеки»: the language of stored TMDB metadata, applied to new scans (absent on older servers). */
  metadataLanguage?: 'ru' | 'en'
  /** «Отправка на Kindle»: the SMTP account books leave from. An answer never carries the password. */
  mail?: MailSettings
  /** A mail password is stored (response only). */
  mailPasswordSet?: boolean
  /** «ИИ-помощник»: the model «Опишите канал» asks (the key is write-only). */
  ai?: AISettings
  aiKeySet?: boolean
  /** None set here, but the server has its own (VAULT_LLM_*): its model. */
  aiFromEnv?: string
  /** What transcodes video now («Перекодирование»'s summary; response only). */
  transcodingName?: string
  /** «Адрес сервера»: how the server is reached from outside the home network ("" none). */
  publicUrl?: string
  /** The server's VAULT_PUBLIC_URL: the address whatever is saved (response only). */
  publicUrlFromEnv?: string
}

/** The hardware of video transcodes: VAULT_HWACCEL modes. */
export type TranscodingMode = 'auto' | 'qsv' | 'vaapi' | 'nvenc' | 'videotoolbox' | 'none'

/** «Настройки сервера → Перекодирование» (GET/PUT /api/settings/transcoding). */
export interface TranscodingSettings {
  mode: TranscodingMode
  /** the render node of VA-API; "" = the first */
  device: string
  tonemap: boolean
  hevc: boolean
}

/**
 * Why a GPU failed its test encode (media.HWProblem): the kind decides the
 * advice — noDevice: the node is not in the container; permission: the
 * server's user is not in the node's group (gid); driver: no VA-API driver
 * takes it (driver: the one the vendor needs); nvidia: no NVIDIA driver in
 * the container; encoder: the driver refuses the encoder; detail: ffmpeg's
 * own words.
 */
export interface HWProblem {
  kind: 'noDevice' | 'permission' | 'driver' | 'nvidia' | 'encoder' | 'timeout' | 'unknown'
  mode: string
  device?: string
  gid?: number
  driver?: string
  encoder?: string
  detail?: string
}

export interface TranscodingView {
  settings: TranscodingSettings
  /** the server's own (VAULT_HWACCEL*) */
  defaults: TranscodingSettings
  /** changed here (else the defaults apply) */
  custom: boolean
  /** the machine's render nodes */
  devices: string[]
  /** what the settings resolved to, and each test encode */
  status: {
    mode: Exclude<TranscodingMode, 'auto'>
    name: string
    requested: string
    device?: string
    hevc: boolean
    tonemapGpu: boolean
    tonemapCpu: boolean
    probes: { mode: string; ok: boolean; error?: string }[]
    /** why the GPU failed its test encode (absent: it passed, or there was none) */
    problem?: HWProblem
    probedAt: string
  }
  /** ffmpeg has chromaprint: the intros and credits of episodes are analysed (absent: no segments worker) */
  fingerprint?: boolean
}

/** One of GET /api/items/{id}/trailers: a YouTube trailer or teaser of the title (TMDB). */
export interface Trailer {
  name: string
  site: 'YouTube'
  key: string
  type: 'Trailer' | 'Teaser'
  lang: string
  official: boolean
  url: string
  /** POST it (playTrailer) to play the trailer from the server; absent: the server does not play trailers (the official embedded player instead). */
  play?: string
}

/** POST /api/trailers/{key}/play: the cached trailer (url carries a media key) and its length. */
export interface TrailerPlay {
  url: string
  durationMs: number
}

/** GET /api/admin/voclean: the cleaned voice-overs' tracks by state. */
export interface VocleanStatus {
  done: number
  /** waiting to be measured or cleaned (retries due included) */
  queued: number
  failed: number
  notEligible: number
  /** «Обработать всю медиатеку»: the backlog runs even while something plays */
  backfill: boolean
}

/** A background worker's progress (bgtask.Progress), «Задачи». */
export interface TaskProgress {
  /** trickplay, segments, loudness (an unknown one is shown by its id) */
  id: string
  /** a pass is under way (it may wait for playback to be quiet) */
  running: boolean
  done: number
  total: number
  /** why it stands still: off (in the settings), full (its space is used up), offline (the media disk is away) */
  state?: 'off' | 'full' | 'offline'
  /** when the last pass ended */
  lastRun?: string
}

/** GET /api/admin/tasks: what runs in the background, in one place. */
export interface TasksView {
  scan?: LibraryStatus
  voclean?: VocleanStatus
  workers: TaskProgress[]
}

/** GET /api/addons/helpers: an enabled addon's helper service, as the addon itself uses it (helpers.status) or asked by the server. */
export interface HelperHealth {
  addon: string
  /** The manifest's compose name («my-helper»). */
  name: string
  /** The manifest's name of it for people, in the interface language (absent: none, `name` is shown). */
  title?: string
  /** Its compose address; '' when `custom` (the admin's own is never shown). */
  address: string
  /** An address of the admin's is in use (a variable, or the addon's own settings). */
  custom?: boolean
  /** Where the address comes from, when the addon told; absent when the server asked by itself. */
  source?: 'override' | 'settings' | 'compose' | 'none'
  ok: boolean
  /** Why it does not answer. */
  problem?: 'dns' | 'refused' | 'timeout' | 'status' | 'auth' | 'unset' | 'other'
  error?: string
  /** When the addon learnt it (RFC 3339). */
  checkedAt?: string
  /** The addon's own last real request, not a probe. */
  live?: boolean
}

/** GET /api/admin/health/network: one service the server reaches out to. */
export interface NetCheck {
  id: string
  name: string
  ok: boolean
  ms: number
  error?: string
}

/** «ИИ-помощник»'s models by weight: each AI task asks the tier that fits it. */
export interface AIModels {
  /** Quick and cheap, many small calls (naming channel categories). */
  light: string
  /** Reading a request («Опишите канал»). */
  medium: string
  /** The hardest tasks. */
  powerful: string
}

export interface AISettings {
  url: string
  /** The medium tier (for a server that knows one model only). */
  model: string
  models?: AIModels
  key?: string
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
  disk: { total: number; used: number; free: number; categories: { id: string; bytes: number; label?: string }[] }
  counts: { movies: number; series: number; episodes: number; albums: number; tracks: number; books: number; audiobooks: number; comics: number }
  watchedByUser: { user: string; avatar: string; photo?: string; hours30d: number }[]
  topItems30d: { id: number; kind: CardKind; title: string; year?: number; poster: string; viewers: number; hours: number }[]
  /** what re-encodes video: mode qsv, vaapi, nvenc, videotoolbox or none (the CPU) */
  transcoding?: { mode: string; name: string; requested: string; device?: string }
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
  type: 'library.updated' | 'disk.warning' | (string & {})
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

/** The addon holding a file or folder: its name, its words for it, and
 * whether it uses the data now. */
export interface FileHoldRef {
  addon: string
  label: string
  active: boolean
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
  hold?: FileHoldRef
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
  categories: { id: string; bytes: number; label?: string }[]
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
  /** Jellyfin, Kavita, Audiobookshelf: reading it over its API instead of its database file. */
  connect?: MigrateConnect
}

/** A source's API connection as the page sees it (never the key or the password). */
export interface MigrateConnect {
  /** apikey: an address and an API key; apikeyOrLogin (Jellyfin): or a user name and a password;
   * login (an addon's source): an address, a user name and a password. */
  kind: 'apikey' | 'apikeyOrLogin' | 'login'
  url: string
  username?: string
  hasSecret: boolean
  connected: boolean
  /** Its database file is mounted too (used while not connected). */
  mounted: boolean
}

/** POST /api/migrate/sources/{id}/connect; a blank key or password keeps the kept one. */
export interface MigrateConnectRequest {
  url: string
  apiKey?: string
  username?: string
  password?: string
}

export type MigrateStatusKind = 'planned' | 'imported' | 'skipped' | 'failed' | 'warning'

export interface MigrateLine {
  source: string
  /** The core's kinds; an addon's migration names its own. */
  kind: 'progress' | 'user' | 'leftover' | (string & {})
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

/** GET /api/import/services: the tiles and what the page may promise. */
export interface ImportServicesReply {
  services: ImportService[]
  /** An addon takes the albums the library lacks (else they are «Нет в медиатеке»). */
  downloads: boolean
  /** The review can search a catalog («Найти вручную», «Выбрать альбом…»). */
  catalog: boolean
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
  /** A device's managed account names its profile («Гостиная · Папа»). */
  ownerName?: string
  avatar: AvatarId
  photo?: string
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
  /** The episode's own 16:9 picture (TMDB still); absent: none (a movie, not fetched yet). */
  still?: string
  /** When it became available (null: not yet). */
  firstSeen: string | null
  kinds: string[]
  why?: FollowWhy
}

export interface CalendarDay {
  date: string
  items: TrackingItem[]
}

// --- Ongoing tracking: a title's episodes with their sources, and the profile's source choice ---

/** What a source offers beyond provider and quality (server tracking.SourceMeta). */
export interface SourceMeta {
  itemId?: number
  fileId?: number
  sourceId?: string
  /** the addon's own id of the source's provider */
  providerId?: string
  voice?: string
  /** audio language, ISO 639-1; absent = unknown */
  lang?: string
  /** Dub | MVO | AVO | Original */
  audio?: string
  studios?: string[]
  subs?: string[]
  fileIndex?: number
}

/** The core's kind (`library`), or an addon's (its scanners' `tracking.scan:<kind>`; opaque to the core). */
export type SourceKind = 'library' | (string & {})

export interface EpisodeSource {
  kind: SourceKind
  ref: string
  /** display name of the provider */
  provider?: string
  quality?: string
  labels: string[]
  firstSeen: string
  meta: SourceMeta
}

export interface TitleEpisode {
  season: number
  number: number
  name: string
  airDate: string
  watched: boolean
  positionSec?: number
  new: boolean
  sources: EpisodeSource[]
}

export interface TitleEpisodes {
  title: { kind: string; tmdbId: number; name: string; poster: string; status: string; itemId?: number }
  episodes: TitleEpisode[]
}

/** A profile's preferred source of a title; every field is optional. */
export interface SourceChoice {
  kind?: SourceKind
  provider?: string
  voice?: string
  lang?: string
  quality?: string
}

// --- accounts: the profile's Shikimori link ---
export type ShikimoriStatus = 'ok' | 'private' | 'not_found' | 'error' | 'relogin'
export type ShikimoriWriteState = 'unasked' | 'asked' | 'enabled' | 'declined'

/** GET /api/accounts → shikimori. */
export interface ShikimoriState {
  linked: boolean
  nickname: string
  status: ShikimoriStatus
  /** The last sync error, in the viewer's language. */
  lastError: string
  lastSync: string | null
  /** A sync of the link runs on the server now (the periodic one, the pull after linking, «Синхронизировать»). */
  syncing: boolean
  writeState: ShikimoriWriteState
  /** An OAuth client is configured and the public URL is https. */
  canWrite: boolean
  /** Remote entries that were not found in TMDB. */
  unmapped: number
}
/** GET /api/accounts → myshows (read only; absent on a server without it). */
export interface MyShowsState {
  linked: boolean
  /** The MyShows login. */
  nickname: string
  status: Exclude<ShikimoriStatus, 'relogin'>
  lastError: string
  lastSync: string | null
  syncing: boolean
  unmapped: number
}
/** GET /api/accounts → goodreads (read only; absent on a server without it). Goodreads has no open API: a linked profile's public shelves are read, and a library export (CSV) is imported. */
export interface GoodreadsState {
  /** A profile is linked (the CSV needs no link). */
  linked: boolean
  /** The name in the profile address. */
  nickname: string
  status: Exclude<ShikimoriStatus, 'relogin' | 'private'>
  lastError: string
  lastSync: string | null
  syncing: boolean
  /** The profile's Goodreads books and, by shelf, how many; `inLibrary` of them are in the library. */
  total: number
  read: number
  reading: number
  toRead: number
  inLibrary: number
}
export interface AccountsState {
  shikimori: ShikimoriState
  myshows?: MyShowsState
  goodreads?: GoodreadsState
}

/** The server's Shikimori OAuth client (admin); the secret is never returned. */
export interface ShikimoriAdminSettings {
  clientId: string
  hasSecret: boolean
  fromEnv: boolean
  redirectUri: string
  scope: string
}

// --- «Расширения» (addons; admin) ---
export type AddonStatus = 'starting' | 'running' | 'crashed' | 'stopped' | 'incompatible'

/** An installed addon (GET /api/addons). */
export interface AddonInfo {
  id: string
  name: string
  description: string
  version: string
  /** The GitHub URL it was installed from. */
  source: string
  enabled: boolean
  status: AddonStatus
  /** A newer release's version, or null. */
  update: string | null
  /** What a null `update` means: '' none newer, 'checking' the server is
   * still asking GitHub (ask again shortly), 'failed' the check failed
   * (`updateError` says why), 'needsCore' a newer release needs a newer
   * server (`updateError`). Missing from older servers. */
  updateState?: '' | 'checking' | 'failed' | 'needsCore'
  updateError?: string
  /** Installed by the deployment (VAULT_ADDONS_PIN): it cannot be
   * updated or removed here, only switched. Missing from older servers. */
  pinned?: boolean
  /** Its pin could not be applied at boot (the server log says why): it
   * stays stopped and cannot be switched on. */
  pinFailed?: boolean
}

/** What an addon's release says about itself, before it is installed. */
export interface AddonPreview {
  id: string
  name: string
  description: string
  version: string
  author: string
  homepage: string
  permissions: string[]
  hasWeb: boolean
  /** The version already installed, '' when it is not. */
  installed: string
}
