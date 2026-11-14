// English strings (tracking group: marks on titles, Calendar, New for you).
import type { EnOf } from '../types'
import type ru from '../ru/tracking'

export default {
  'tracking.mark.none': 'Mark',
  'tracking.mark.aria': 'Mark',
  'tracking.mark.planned': 'Want to watch',
  'tracking.mark.watching': 'Watching',
  'tracking.mark.completed': 'Watched',
  'tracking.mark.on_hold': 'On hold',
  'tracking.mark.dropped': 'Dropped',
  'tracking.mark.clear': 'Remove mark',
  'tracking.mark.failed': "Couldn't save the mark. Try again.",
  'tracking.calendar.title': 'Calendar',
  'tracking.calendar.range': 'Range',
  'tracking.calendar.week': 'Week',
  'tracking.calendar.month': 'Month',
  'tracking.calendar.prev': 'Earlier',
  'tracking.calendar.next': 'Later',
  'tracking.calendar.today': 'Today',
  'tracking.calendar.available': 'available',
  'tracking.calendar.empty': 'Nothing comes out in these days. Mark a series «Watching» or «Want to watch» and its new episodes show up here.',
  'tracking.calendar.failed': "Couldn't load the calendar.",
  'tracking.calendar.episode': 'S{season} E{episode}',
  'tracking.calendar.movie': 'Movie',
} satisfies EnOf<typeof ru>
