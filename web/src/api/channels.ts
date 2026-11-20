// «ТВ-каналы» (/api/channels…): live channels made of the library, their
// guide and the editor's options.
import { qs, request } from "./client";
import type { Quality, QualityInfo } from "./types";

export type ChannelOrder = "shuffle" | "ordered";

/**
 * One entry of a channel's own list (rules.picks): a movie or a series
 * (`title`, a series plays all its episodes in order), one movie or episode
 * (`item`), or every video under a library folder (`folder`, the path as the
 * folder browser returns it).
 */
export type ChannelPick =
  | { kind: "title"; id: number }
  | { kind: "item"; id: number }
  | { kind: "folder"; path: string };

/** What a channel plays (the server's channels.Rules). */
export interface ChannelRules {
  kinds?: ("movie" | "series")[];
  genres?: string[];
  /** None of these genres (the generated «Все сериалы» keeps animation out; the editor has no field for it, it carries it through). */
  notGenres?: string[];
  yearFrom?: number;
  yearTo?: number;
  /** Made in any of these countries (ISO 3166-1, «SU» the Soviet Union). */
  countries?: string[];
  /** The original language is any of these (ISO 639-1). */
  languages?: string[];
  /** Themes: TMDB keywords (English, lower case). */
  keywords?: string[];
  /** Round 1's chosen titles (read only: the editor saves them as picks). */
  titles?: number[];
  /** The channel's own ordered list; when set it is the whole pool (the filters are ignored). */
  picks?: ChannelPick[];
  libraryUser?: number;
  maxRating?: string;
  order?: ChannelOrder;
  /** The audio language the channel prefers (ISO 639; no field in the editor, carried through). */
  audioLang?: string;
  /** Also films of these genres the library lacks, from an addon's titles (where the server offers it). */
  extras?: boolean;
  /** Episodes play without their recap and intro and end where their credits start (movies play whole). */
  skipSegments?: boolean;
  /** «С докачкой» (an admin's): these titles in order, the ones not on disk fetched before they air and removed after. */
  fetch?: ChannelFetchRules;
}

/** An audio or subtitle rendition of the live stream present in a programme. */
export interface ChannelTrack {
  /** Round 3's fixed slot (older servers; never relied on). */
  slot?: string;
  /** The HLS rendition's NAME, unique in its group (select the rendition by it). */
  name: string;
  /** What the picker shows («Русский — LostFilm»); may be longer than the name. */
  label: string;
  /** BCP 47 / ISO 639 of the track ("" unknown). */
  lang: string;
  /** A cleaned voice-over: the NAME of the voice-over it is the cleaned version of (in this programme). */
  cleanedOf?: string;
}

export interface ChannelProgram {
  /** "<channel>-<seq>" (older servers: absent). */
  id?: string;
  itemId: number;
  /** 'external': an external playlist's programme (no item page, itemId 0). */
  kind?: "movie" | "episode" | "external";
  title: string;
  subtitle?: string;
  overview?: string;
  year?: number;
  rating?: string;
  genres?: string[];
  start: string;
  end: string;
  poster?: string;
  backdrop?: string;
  /** From an addon, not in the library (itemId is negative). */
  external?: boolean;
  /** Absent: unknown; empty: none. */
  audio?: ChannelTrack[];
  subtitles?: ChannelTrack[];
}

/** What a stream is: an HLS playlist, a raw MPEG-TS stream, or '' (unknown: try HLS, then MPEG-TS). */
export type StreamFormat = "hls" | "ts" | "";

/** Where an external channel's stream is, as the server tells this client. */
export interface ExternalStream {
  /** The provider's URL, or /api/channels/live/ext/{id}/{n} (a redirect) when the source hides addresses. */
  url: string;
  /** The server's same-origin relay of it (/api/channels/live/ext/{id}/{n}/relay): what the web plays when it cannot play `url`. */
  relay?: string;
  format?: StreamFormat;
  userAgent?: string;
  referrer?: string;
  /** The platforms that cannot play it (a header a browser cannot send, http on an https page…). */
  unsupported: string[];
  fallbacks: {
    url: string;
    relay?: string;
    format?: StreamFormat;
    userAgent?: string;
    referrer?: string;
    unsupported?: string[];
  }[];
}

export interface Channel {
  id: number;
  number: number;
  name: string;
  category: string;
  personal: boolean;
  auto: boolean;
  enabled: boolean;
  canEdit: boolean;
  /** Starred by this profile. */
  favorite?: boolean;
  /** The uploaded logo ("/api/channels/{id}/logo.png?v=…"); absent: none (show number and name). */
  logoUrl?: string;
  /** An external channel's guide icon when logoUrl is another one: shown when that one does not load. */
  logoAlt?: string;
  /** The logo was found by the server (a franchise's or a series' title logo). */
  logoAuto?: boolean;
  /** Only for a channel the profile may edit. */
  rules?: ChannelRules;
  /** The editor's list: rules.picks described in order (only where the profile may edit). */
  pickInfo?: ChannelPickInfo[];
  now?: ChannelProgram;
  next?: ChannelProgram;
  /** From an external playlist: the client plays `stream` itself. */
  external?: boolean;
  stream?: ExternalStream;
  /** Its category ids (ChannelCategory.id). */
  categories?: string[];
}

/**
 * A category of the viewer's list: `ai:<code>` (the AI helper's, named in the
 * request's language), `source:<id>` (an external source, when there are two or
 * more), `group:<name>` (a group of none of the topics). The server keeps the
 * most common topics and groups only, one spelling each («Кино», «Фильмы» and
 * «Movies» are one). `ai:adult` is exclusive: its channels are in no other
 * category, nor in «Все» and «Избранное»; it comes last.
 */
export interface ChannelCategory {
  id: string;
  name: string;
  kind: "ai" | "source" | "group";
  count: number;
  /** Its channels are in no other list (the adult category). */
  exclusive?: boolean;
}

export interface ChannelList {
  /** In number order: the one list. */
  channels: Channel[];
  /** The channel this profile tuned in last (a TV's «last channel»). */
  lastChannel?: number | null;
  canManage: boolean;
  canCreate: boolean;
  /** Every category of the list once, in the filter's order (older servers: absent). */
  categories?: ChannelCategory[];
  /** The profile lists its starred channels first (older servers: absent, number order). */
  favoritesFirst?: boolean;
  /** The manage list only: the deleted automatic channels this profile may give back. */
  removed?: RemovedChannel[];
}

/** An automatic channel that was deleted: the generator skips it until it is given back. */
export interface RemovedChannel {
  id: number;
  name: string;
  category: string;
  personal: boolean;
}

export interface ChannelGuide {
  channel: Channel;
  programs: ChannelProgram[];
}

/** A channel in the guide grid. */
export interface ChannelEPG {
  id: number;
  number: number;
  name: string;
  category: string;
  favorite?: boolean;
  logoUrl?: string;
  /** The guide icon when logoUrl is another one (see Channel.logoAlt). */
  logoAlt?: string;
  programs: ChannelProgram[];
  categories?: string[];
}

export interface ChannelEPGWindow {
  from: string;
  to: string;
  channels: ChannelEPG[];
}

export interface ChannelOptions {
  genres: { key: string; name: string }[];
  decades: number[];
  titles: {
    id: number;
    kind: "movie" | "series";
    title: string;
    year?: number;
    episodes?: number;
  }[];
  profiles: { id: number; name: string }[];
  /** The server can mix in films from outside the library (rules.extras). */
  extrasAvailable?: boolean;
  /** What the library has to pick by, the most common first (older servers: none). */
  countries?: TagCount[];
  languages?: TagCount[];
  keywords?: TagCount[];
  yearMin?: number;
  yearMax?: number;
  /** «Опишите канал» has a language model behind it (else the words alone). */
  aiAvailable?: boolean;
  /** An admin may make a channel with fetching (an addon downloads, TMDB names the titles). */
  fetchAvailable?: boolean;
  /** The disk every channel with fetching may take together (GB). */
  fetchGlobalGB?: number;
}

export interface TagCount {
  key: string;
  count: number;
}

/** What a channel of some rules would play now (POST /api/channels/preview). */
export interface ChannelPreviewResult {
  movies: number;
  series: number;
  episodes: number;
  items: {
    id: number;
    kind: "movie" | "series";
    title: string;
    year?: number;
    poster?: string;
  }[];
}

/** A channel read from a sentence (POST /api/channels/suggest). */
export interface ChannelSuggestion {
  name: string;
  rules: ChannelRules;
  via: "words" | "ai";
}

/** A pick as the server describes it (Channel.pickInfo). */
export type ChannelPickInfo = ChannelPick & {
  title: string;
  subtitle?: string;
  year?: number;
  /** Nothing of it plays now (gone, above the age limit…). */
  missing?: boolean;
};

/** A hit of the editor's search (movies, series, episodes). */
export interface ChannelSearchHit {
  kind: "movie" | "series" | "episode";
  id: number;
  title: string;
  /** An episode's «Series · S1 E2»… */
  subtitle?: string;
  year?: number;
  /** What to put in rules.picks. */
  pick?: ChannelPick;
}

/** One level of the library's folders for the editor's browser. */
export interface ChannelFolderLevel {
  folders: { path: string; name: string }[];
  /** Playable files here: their item ids. */
  files: { id: number; name: string; kind?: "movie" | "episode" }[];
}

export interface ChannelInput {
  name: string;
  number?: number;
  category: string;
  personal: boolean;
  enabled: boolean;
  rules: ChannelRules;
}

/**
 * The channel list. `brief`: now and next without their descriptions and
 * tracks (a big playlist's list is thousands of rows; the details pane and
 * the player read those from the channel's guide).
 */
export const getChannels = (signal?: AbortSignal, manage = false, brief = false) =>
  request<ChannelList>(
    "GET",
    `/api/channels${qs({ manage: manage ? 1 : undefined, brief: brief ? 1 : undefined })}`,
    undefined,
    signal,
  );

/** The profile's own choices for its channel list. */
export interface ChannelPrefs {
  /** The starred channels stand at the top of the list (default on). */
  favoritesFirst: boolean;
}

export const getChannelPrefs = (signal?: AbortSignal) =>
  request<ChannelPrefs>("GET", "/api/channels/prefs", undefined, signal);

export const setChannelPrefs = (p: ChannelPrefs) =>
  request<ChannelPrefs>("PUT", "/api/channels/prefs", p);

export const getChannelGuide = (id: number, hours = 12, signal?: AbortSignal) =>
  request<ChannelGuide>(
    "GET",
    `/api/channels/${id}/guide${qs({ hours })}`,
    undefined,
    signal,
  );

/** The guide grid: every channel with the programmes overlapping [from, from+hours). */
export const getChannelEPG = (from: Date, hours = 6, signal?: AbortSignal) =>
  request<ChannelEPGWindow>(
    "GET",
    `/api/channels/epg${qs({ from: from.toISOString().replace(/\.\d{3}Z$/, "Z"), hours })}`,
    undefined,
    signal,
  );

/** Star or unstar a channel for this profile. */
export const setChannelFavorite = (id: number, on: boolean) =>
  request<void>(on ? "PUT" : "DELETE", `/api/channels/${id}/favorite`);

/** The channel was tuned in: the profile's «last channel» (never progress or history). */
export const setLastChannel = (id: number) =>
  request<void>("PUT", "/api/channels/last", { channelId: id });

export const previewChannel = (rules: ChannelRules, signal?: AbortSignal) =>
  request<ChannelPreviewResult>(
    "POST",
    "/api/channels/preview",
    { rules },
    signal,
  );
export const suggestChannel = (text: string, signal?: AbortSignal) =>
  request<ChannelSuggestion>("POST", "/api/channels/suggest", { text }, signal);
export const getChannelOptions = (signal?: AbortSignal) =>
  request<ChannelOptions>("GET", "/api/channels/options", undefined, signal);

export const searchChannelOptions = (q: string, signal?: AbortSignal) =>
  request<{ results?: ChannelSearchHit[] } | ChannelSearchHit[]>(
    "GET",
    `/api/channels/options/search${qs({ q })}`,
    undefined,
    signal,
  ).then((r) => (Array.isArray(r) ? r : (r?.results ?? [])));

/** The folders and files under `path` ("" : the library roots). */
export const getChannelFolders = (path: string, signal?: AbortSignal) =>
  request<ChannelFolderLevel>(
    "GET",
    `/api/channels/options/folders${qs({ path: path || undefined })}`,
    undefined,
    signal,
  ).then((r) => ({
    folders: r?.folders ?? [],
    files: r?.files ?? [],
  }));

/** The largest logo the server takes. */
export const LOGO_MAX_BYTES = 2 * 1024 * 1024;
export const LOGO_TYPES = ["image/png", "image/jpeg", "image/webp"];

/** Upload a channel's logo (PNG, JPEG or WebP ≤ 2 MB): the channel as it is now, or nothing. */
export function uploadChannelLogo(id: number, file: File) {
  const body = new FormData();
  body.append("file", file);
  return request<Partial<Channel> | undefined>(
    "POST",
    `/api/channels/${id}/logo`,
    body,
  );
}
export const deleteChannelLogo = (id: number) =>
  request<void>("DELETE", `/api/channels/${id}/logo`);

export const createChannel = (c: ChannelInput) =>
  request<Channel>("POST", "/api/channels", c);
export const updateChannel = (id: number, c: ChannelInput) =>
  request<Channel>("PUT", `/api/channels/${id}`, c);
/** Deletes a channel; an automatic one is also remembered as deleted (`removed` of the manage list). */
export const deleteChannel = (id: number) =>
  request<void>("DELETE", `/api/channels/${id}`);
/** Gives a deleted automatic channel back; `restored` false: the library has not enough for it yet, it is made when it has. */
export const restoreChannel = (id: number) =>
  request<{ restored: boolean }>(
    "POST",
    `/api/channels/removed/${id}/restore`,
  );
/** Brings the automatic channels up to date; `reset` rebuilds them (deleted ones back, ones the library has not enough for dropped; made or edited ones stay). */
export const generateChannels = (reset = false) =>
  request<{ created: number; updated: number; dropped: number }>(
    "POST",
    reset ? "/api/channels/generate?reset=1" : "/api/channels/generate",
  );

/** The web player's live stream of a channel, in a quality of the film player's menu («Авто»: the server decides, as for a film). */
export const liveUrl = (id: number, quality: Quality = "auto") =>
  `/api/channels/live/${id}/index.m3u8${quality === "auto" ? "" : `?q=${quality}`}`;

/** What the live player's «Качество» means right now (as a film's: «Авто» at home is the channel's own stream). */
export const getLiveQuality = (quality: Quality, signal?: AbortSignal) =>
  request<QualityInfo>("GET", `/api/channels/live/quality${qs({ q: quality })}`, undefined, signal);

/** How far a programme is (0…1) at `now`. */
export function programProgress(p: ChannelProgram, now = Date.now()): number {
  const s = Date.parse(p.start);
  const e = Date.parse(p.end);
  if (!(e > s)) return 0;
  return Math.min(1, Math.max(0, (now - s) / (e - s)));
}

/** A title of a franchise («Франшизы»), in its order; hidden: the admin left it out. */
export interface FranchiseItem {
  id: number;
  kind: "movie" | "series";
  title: string;
  year?: number;
  hidden?: boolean;
}

/** A franchise the franchise channels play (admin): a TMDB collection, titles sharing a Wikidata franchise, or those in the AI helper's story order. */
export interface Franchise {
  key: string;
  name: string;
  source: "tmdb" | "wikidata" | "ai";
  /** Why the AI helper's order is not used. */
  error?: string;
  /** The admin's order is set (it survives recomputes). */
  manual: boolean;
  /** The order it plays, then the hidden titles. */
  items: FranchiseItem[];
  /** Its channel (none: too little of it in the library). */
  channelId?: number;
  /** Its channel was deleted in «Управление каналами»: it comes back only when given back there. */
  channelRemoved?: boolean;
  /** Wikidata ties none of its titles to a franchise: nothing could merge it with another. */
  untied?: boolean;
}

/** What the franchises could be merged by: Wikidata on, the library's titles, read in Wikidata, tied to a franchise there. */
export interface FranchiseEvidence {
  wikidata: boolean;
  titles: number;
  read: number;
  tied: number;
}

export interface FranchiseStatus {
  running: boolean;
  at?: string;
  /** The AI helper's model of the last pass ("" = none: TMDB order). */
  model?: string;
  error?: string;
  /** TMDB did not answer for some titles: tried again later. */
  incomplete?: boolean;
}

export const getFranchises = (signal?: AbortSignal) =>
  request<{ status: FranchiseStatus; available: boolean; franchises: Franchise[]; evidence?: FranchiseEvidence }>(
    "GET",
    "/api/channels/franchises",
    undefined,
    signal,
  );

export const refreshFranchises = () =>
  request<void>("POST", "/api/channels/franchises/refresh");

export const setFranchiseOrder = (key: string, order: number[], hidden: number[]) =>
  request<Franchise>("PUT", `/api/channels/franchises/${encodeURIComponent(key)}/order`, { order, hidden });

export const resetFranchiseOrder = (key: string) =>
  request<Franchise>("DELETE", `/api/channels/franchises/${encodeURIComponent(key)}/order`);

/** A title of a channel with fetching: a TMDB movie, or a series (season 0: all of them). */
export interface ChannelFetchTitle {
  kind: "movie" | "series";
  tmdbId: number;
  season?: number;
  title?: string;
  year?: number;
}

export interface ChannelFetchRules {
  titles: ChannelFetchTitle[];
  /** The disk the channel's fetches may take (0 / none: the server's default). */
  budgetGB?: number;
  /** How long a fetched programme stays after it aired (0 / none: the default). */
  graceHours?: number;
}

/** The server's defaults (channels.DefaultFetch*). */
export const FETCH_DEFAULT_CHANNEL_GB = 50;
export const FETCH_DEFAULT_GLOBAL_GB = 200;
export const FETCH_DEFAULT_GRACE_HOURS = 24;

export type ChannelFetchState = "wanted" | "requested" | "ready" | "library" | "kept" | "removed" | "failed";

export interface ChannelFetchProgramme {
  kind: "movie" | "episode";
  title: string;
  episodeTitle?: string;
  season?: number;
  episode?: number;
  state: ChannelFetchState;
  size?: number;
  done?: number;
  error?: string;
  /** Its next slot in the schedule (none within the 48 h horizon). */
  airs?: string;
}

export interface ChannelFetchUsage {
  used: number;
  budget: number;
}

export interface ChannelFetchStatus {
  available: boolean;
  channel: ChannelFetchUsage;
  global: ChannelFetchUsage;
  globalGB: number;
  programmes: ChannelFetchProgramme[];
}

export interface ChannelFetchFound {
  kind: "movie" | "series";
  tmdbId: number;
  title: string;
  year?: number;
  poster?: string;
}

/** A channel with fetching: its disk use, the budgets and each programme (admins). */
export const getChannelFetch = (id: number, signal?: AbortSignal) =>
  request<ChannelFetchStatus>("GET", `/api/channels/${id}/fetch`, undefined, signal).then((r) => ({
    ...r,
    programmes: r?.programmes ?? [],
  }));

/** The disk every channel's fetches may take together (0: the default). */
export const setChannelFetchBudget = (globalGB: number) =>
  request<{ globalGB: number }>("PUT", "/api/channels/fetch/budget", { globalGB });

/** Movies and series to add to a channel with fetching (TMDB). */
export const searchChannelFetch = (q: string, signal?: AbortSignal) =>
  request<ChannelFetchFound[]>("GET", `/api/channels/fetch/search${qs({ q })}`, undefined, signal).then((r) => r ?? []);

/** A series' seasons (specials left out). */
export const getChannelFetchSeasons = (tmdbId: number, signal?: AbortSignal) =>
  request<number[]>("GET", `/api/channels/fetch/seasons${qs({ tmdbId })}`, undefined, signal).then((r) => r ?? []);
