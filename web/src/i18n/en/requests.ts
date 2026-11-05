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
  'requests.lead': 'What the kids ask to add to the library. Approve confirms the request, and the kid sees it when the title arrives in the library.',
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

  // the sources build's, but named by core code (were in ./full.ts)
  'requests.leadFull': 'What the kids ask to add. Approve opens the release picker: once the download starts the request is approved, and the kid sees it when it arrives in the library.',
} satisfies EnOf<typeof ru>
