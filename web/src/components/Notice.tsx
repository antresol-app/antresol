// The short confirmation of state/notice.ts, at the bottom of the screen (a Toast).
import { useNotice } from '../state/notice'
import { Toast } from '../ui/Toast'

export function Notice() {
  const n = useNotice()
  // the Toast's live region stays in the DOM, so screen readers announce each text
  return <Toast messageKey={n?.key}>{n?.text}</Toast>
}
