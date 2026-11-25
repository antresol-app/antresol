// English strings (requests group: Ask of kid profiles and the admin's Requests).
import type { EnOf } from '../types'
import type ru from '../ru/requests'

export default {
  'manage.section.requests': 'Requests',
  'requests.ask': 'Ask',
  'requests.askFor': 'Ask for “{title}”',
  'requests.askFailed': 'Couldn’t send the request. Try again.',
  'requests.pending': 'Waiting for an answer',
  'requests.approved': 'Approved — coming soon',
  'requests.inLibrary': 'Already in the library',
  'requests.declined': 'A parent said no',
  'requests.reasonLine': '“{reason}”',
  'requests.mine': 'My Requests',
  'comp.notify.requests': 'My requests approved / in the library',
  'requests.hidden': 'Not available for this profile',
  'requests.mineEmpty': 'What you ask for will show up here. Find a movie, show, music or a book in Search and tap Ask.',
  'requests.title': 'Requests',
  'requests.lead': 'Approve a request and add the title to the library: the kid sees it as soon as it arrives.',
  'requests.switch': 'Kids can ask',
  'requests.switch.hint': 'A kid finds a movie or show the library lacks in Search and taps Ask. You answer here.',
  'requests.waiting': 'Waiting for an answer',
  'requests.decided': 'Answered in the last 30 days',
  'requests.empty': 'No new requests.',
  'requests.from': '{name} · {when}',
  'requests.approve': 'Approve',
  'requests.approveFor': 'Approve “{title}”',
  'requests.decline': 'Decline',
  'requests.declineFor': 'Decline “{title}”',
  'requests.declineTitle': 'Decline “{title}”?',
  'requests.declineMessage': '{name} will see that you declined. You can briefly say why.',
  'requests.reason': 'Reason (optional)',
  'requests.declineFailed': 'Couldn’t decline. Try again.',
  'requests.admin.approved': 'Approved',
  'requests.admin.declined': 'Declined',
  'requests.badge': { one: '{count} request waiting', other: '{count} requests waiting' },
  'requests.approveFailed': 'Couldn’t approve. Try again.',
  'requests.sheetFailed': 'The extension couldn’t open its choice. Try again.',

  // a kid's search for something to ask for (Search, RequestSearch)
  'requests.search.title': 'Find and Ask',
  'requests.search.video': 'Movies & TV',
  'requests.search.failed': 'Search isn’t available right now',
  'requests.search.nothingMore': 'Nothing else found.',
  'requests.search.searching': 'Searching',

  // the sources build's, but named by core code (were in ./full.ts)
} satisfies EnOf<typeof ru>
