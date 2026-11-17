// English strings (the episode guide: sources of an episode, the source picker).
import type { EnOf } from '../types'
import type ru from '../ru/episodes'

export default {
  'episodes.guide': 'Episodes',
  'episodes.new': 'new',
  'episodes.watched': 'watched',
  'episodes.progress': 'resume at {time}',
  'episodes.upcoming': 'airs {date}',
  'episodes.noSources': 'no sources',
  'episodes.choose': 'Choose a source',
  'episodes.moreTags': '+{n}',
  'episodes.kind.library': 'Disk',
  'episodes.kind.online': 'Online',
  'episodes.sheet.title': 'Episode {n} · {name}',
  'episodes.sheet.otherLang': 'Other',
  'episodes.sheet.subs': 'Subtitles',
  'episodes.sheet.other': 'Other sources…',
  'episodes.sheet.download': 'Download episode',
  'episodes.sheet.toDevice': 'To this device',
} satisfies EnOf<typeof ru>
