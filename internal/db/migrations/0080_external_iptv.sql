-- «ТВ»: external IPTV playlists and guides (internal/iptv).
-- Every external logical channel is a row of channels with
-- kind='external'; numbers, favourites, last channel and logos work for it
-- unchanged. number_manual: an admin set the number (automatic numbering
-- leaves it). why: what decided the row's visibility (shown in the editor).
ALTER TABLE channels ADD COLUMN kind TEXT NOT NULL DEFAULT 'antresol';
ALTER TABLE channels ADD COLUMN number_manual INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN why TEXT NOT NULL DEFAULT '';

-- A source: an M3U by URL or an Xtream server. url_enc and
-- xtream_pass_enc are sealed (AES-GCM, server key). default_shown NULL:
-- decided by the playlist's size. profiles_json: the profiles that see the
-- source ([] = every profile without limits). Times are Unix ms (0: never).
-- pending_drop: channels a refresh would lose, waiting for confirmation.
CREATE TABLE iptv_sources (
  id               INTEGER PRIMARY KEY,
  name             TEXT NOT NULL,
  kind             TEXT NOT NULL DEFAULT 'm3u',
  url_enc          BLOB NULL,
  xtream_server    TEXT NOT NULL DEFAULT '',
  xtream_user      TEXT NOT NULL DEFAULT '',
  xtream_pass_enc  BLOB NULL,
  default_shown    INTEGER NULL,
  hide_urls        INTEGER NOT NULL DEFAULT 0,
  preset_timeshift INTEGER NOT NULL DEFAULT 1,
  preset_adult     INTEGER NOT NULL DEFAULT 1,
  refresh_hours    INTEGER NOT NULL DEFAULT 24,
  refresh_on_open  INTEGER NOT NULL DEFAULT 0,
  position         INTEGER NOT NULL DEFAULT 0,
  profiles_json    TEXT NOT NULL DEFAULT '[]',
  last_ok_ms       INTEGER NOT NULL DEFAULT 0,
  last_try_ms      INTEGER NOT NULL DEFAULT 0,
  last_err         TEXT NOT NULL DEFAULT '',
  pending_drop     INTEGER NOT NULL DEFAULT 0
);

-- A source's guide URLs, position = priority. auto: taken from the
-- playlist's url-tvg (replaced on each refresh), else added by an admin.
CREATE TABLE iptv_epg_urls (
  id              INTEGER PRIMARY KEY,
  source_id       INTEGER NOT NULL REFERENCES iptv_sources(id) ON DELETE CASCADE,
  url_enc         BLOB NOT NULL,
  position        INTEGER NOT NULL DEFAULT 0,
  refresh_hours   INTEGER NOT NULL DEFAULT 24,
  refresh_on_open INTEGER NOT NULL DEFAULT 0,
  auto            INTEGER NOT NULL DEFAULT 0,
  last_ok_ms      INTEGER NOT NULL DEFAULT 0,
  last_try_ms     INTEGER NOT NULL DEFAULT 0,
  last_err        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX iptv_epg_urls_source ON iptv_epg_urls(source_id);

-- A playlist entry of a source. ident: tvg:<id> | url:<url without
-- credentials> | name:<normalised name>, the key manual decisions survive
-- renames by. channel_id: its logical channel (channels.kind='external').
-- url_enc: the stream URL, sealed. epg_id: the matched guide channel of
-- guide epg_url_id (0: any guide of the source, by priority; epg_how:
-- tvg-id|name|norm-name|manual). manual_json: the admin's decision
-- (iptv.Decision, '' = none). shown, why: the decision in effect (rules,
-- then the manual one). present: in the last good playlist.
CREATE TABLE iptv_members (
  id            INTEGER PRIMARY KEY,
  source_id     INTEGER NOT NULL REFERENCES iptv_sources(id) ON DELETE CASCADE,
  channel_id    INTEGER NULL REFERENCES channels(id) ON DELETE SET NULL,
  ident         TEXT NOT NULL,
  tvg_id        TEXT NOT NULL DEFAULT '',
  tvg_name      TEXT NOT NULL DEFAULT '',
  name          TEXT NOT NULL DEFAULT '',
  grp           TEXT NOT NULL DEFAULT '',
  logo          TEXT NOT NULL DEFAULT '',
  url_enc       BLOB NULL,
  user_agent    TEXT NOT NULL DEFAULT '',
  referrer      TEXT NOT NULL DEFAULT '',
  epg_id        TEXT NOT NULL DEFAULT '',
  epg_url_id    INTEGER NOT NULL DEFAULT 0,
  epg_how       TEXT NOT NULL DEFAULT '',
  epg_manual    INTEGER NOT NULL DEFAULT 0,
  manual_json   TEXT NOT NULL DEFAULT '',
  shown         INTEGER NOT NULL DEFAULT 0,
  why           TEXT NOT NULL DEFAULT '',
  present       INTEGER NOT NULL DEFAULT 1,
  position      INTEGER NOT NULL DEFAULT 0,
  first_seen_ms INTEGER NOT NULL DEFAULT 0,
  UNIQUE(source_id, ident)
);
CREATE INDEX iptv_members_channel ON iptv_members(channel_id);

-- Programmes of matched channels only, window now-1 day … now+7 days.
CREATE TABLE iptv_programmes (
  epg_url_id  INTEGER NOT NULL REFERENCES iptv_epg_urls(id) ON DELETE CASCADE,
  xml_channel TEXT NOT NULL,
  start_ms    INTEGER NOT NULL,
  end_ms      INTEGER NOT NULL,
  title       TEXT NOT NULL DEFAULT '',
  sub_title   TEXT NOT NULL DEFAULT '',
  descr       TEXT NOT NULL DEFAULT '',
  category    TEXT NOT NULL DEFAULT '',
  icon        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX iptv_programmes_channel ON iptv_programmes(xml_channel, end_ms);
CREATE INDEX iptv_programmes_url ON iptv_programmes(epg_url_id);

-- Every channel of a guide (its display-names), for matching and the
-- admin's search when binding by hand.
CREATE TABLE iptv_xml_channels (
  epg_url_id INTEGER NOT NULL REFERENCES iptv_epg_urls(id) ON DELETE CASCADE,
  xml_id     TEXT NOT NULL,
  names_json TEXT NOT NULL DEFAULT '[]',
  icon       TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (epg_url_id, xml_id)
);

-- A set: which sources (and, optionally, which of their groups/channels
-- not) a device or a link sees. Set 0 (no row) = «all my channels».
CREATE TABLE iptv_sets (
  id                   INTEGER PRIMARY KEY,
  name                 TEXT NOT NULL,
  sources_json         TEXT NOT NULL DEFAULT '[]',
  hidden_groups_json   TEXT NOT NULL DEFAULT '[]',
  hidden_channels_json TEXT NOT NULL DEFAULT '[]'
);

-- The set of a profile's app device (device_id '' = the profile's default
-- link).
CREATE TABLE iptv_set_assign (
  user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id TEXT NOT NULL DEFAULT '',
  set_id    INTEGER NOT NULL REFERENCES iptv_sets(id) ON DELETE CASCADE,
  PRIMARY KEY (user_id, device_id)
);

-- An IPTV link made for one TV: its set (NULL: the device/default set).
ALTER TABLE app_tokens ADD COLUMN set_id INTEGER NULL REFERENCES iptv_sets(id) ON DELETE SET NULL;
