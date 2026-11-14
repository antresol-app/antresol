// English strings (pairing group: sign-in on a TV by QR code, the page /pair).
import type { EnOf } from '../types'
import type ru from '../ru/pairing'

export default {
  'pair.title': 'Sign In on a TV',
  'pair.lead': 'The TV shows a code. Approve the sign-in here and it opens the chosen profile — no password on the remote.',
  'pair.code': 'Code on the TV screen',
  'pair.codeHint': '6 letters and digits',
  'pair.continue': 'Continue',
  'pair.device': 'Device',
  'pair.model': 'Model',
  'pair.profile': 'Profile',
  'pair.unnamed': 'Unnamed',
  'pair.remoteTitle': 'This device is not on your home network',
  'pair.remote': 'Approve only if you are setting it up yourself.',
  'pair.approve': 'Approve Sign-In',
  'pair.deny': 'Decline',
  'pair.approvedTitle': 'Sign-in approved',
  'pair.approved': 'The TV is opening the profile “{name}”.',
  'pair.deniedTitle': 'Sign-in declined',
  'pair.denied': 'If this is your TV, it will show a new code.',
  'pair.failed': 'That did not work. Please try again.',
  'pair.another': 'Enter Another Code',
} satisfies EnOf<typeof ru>
