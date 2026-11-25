// Registers the service worker (production builds; Cache Storage and
// service workers need HTTPS or localhost) and tells it when the saved
// list changes.
import { CHANGED_MESSAGE, KEEP_PDF_MESSAGE, WIPED_MESSAGE } from './saved'

export function registerSW() {
  if (!import.meta.env.PROD || typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return
  const keep = (w?: ServiceWorker | null) => w?.postMessage({ type: KEEP_PDF_MESSAGE })
  const go = () =>
    navigator.serviceWorker
      .register('/sw.js')
      // at every start, and when a new worker takes over: it keeps the PDF reader's files for a saved PDF
      .then(() => navigator.serviceWorker.ready)
      .then((r) => keep(r.active))
      .catch((e: unknown) => {
        console.warn('vault: service worker not registered', e)
      })
  navigator.serviceWorker.addEventListener('controllerchange', () => keep(navigator.serviceWorker.controller))
  if (document.readyState === 'complete') void go()
  else window.addEventListener('load', () => void go(), { once: true })
}

/** The service worker re-reads which files are saved (`wiped`: forgets them all at once). */
export function notifySW(wiped = false) {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return
  const msg = { type: wiped ? WIPED_MESSAGE : CHANGED_MESSAGE }
  navigator.serviceWorker.controller?.postMessage(msg)
  void navigator.serviceWorker
    .getRegistration()
    .then((r) => r?.active?.postMessage(msg))
    .catch(() => undefined)
}
