// The English catalog: every namespace merged. Adding a language = a new folder like this one.
// As in ../ru.ts, the sources build's strings are not in the core build.
import type { ru } from './ru'
import type { EnOf } from './types'
import common from './en/common'
import app from './en/app'
import components from './en/components'
import browse from './en/browse'
import music from './en/music'
import player from './en/player'
import books from './en/books'
import files from './en/files'
import manageSettings from './en/manageSettings'
import manageLibrary from './en/manageLibrary'
import requests from './en/requests'
import migrate from './en/migrate'
import playlistImport from './en/playlistImport'
import channels from './en/channels'
import full from './en/full'

export const en: EnOf<typeof ru> = {
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
  ...(__LIBRARY_EDITION__ ? ({} as typeof full) : full),
}
