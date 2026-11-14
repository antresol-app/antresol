// The Russian (source) catalog: every namespace merged. Adding a language = a new folder like this one.
// The sources build's strings (./ru/full) are left out of the core build's
// bundle: the spread is `{}` there, typed as the full catalog so keys stay
// one type (docs/build-tags.md). In the public tree ./ru/full is a stub
// (./ru/full.stub.ts): no strings, only the names of the sources build's keys
// that core code names (in branches the core build never runs).
import common from './ru/common'
import app from './ru/app'
import components from './ru/components'
import browse from './ru/browse'
import music from './ru/music'
import player from './ru/player'
import books from './ru/books'
import files from './ru/files'
import manageSettings from './ru/manageSettings'
import manageLibrary from './ru/manageLibrary'
import requests from './ru/requests'
import migrate from './ru/migrate'
import playlistImport from './ru/playlistImport'
import channels from './ru/channels'
import iptv from './ru/iptv'
import tracking from './ru/tracking'
import full from './ru/full'

export const ru = {
  ...common,
  ...app,
  ...components,
  ...browse,
  ...music,
  ...player,
  ...books,
  ...files,
  ...manageSettings,
  ...manageLibrary,
  ...requests,
  ...migrate,
  ...playlistImport,
  ...channels,
  ...iptv,
  ...tracking,
  ...(__LIBRARY_EDITION__ ? ({} as typeof full) : full),
}
