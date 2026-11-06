import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './design/tokens.css'
import './design/glass.css'
import './design/motion.css'
import { App } from './app/App'
import { registerSW } from './offline/register'
import { bindOfflineToSession } from './offline/store'
import { getLanguage, loadCatalogAtBoot } from './i18n'

registerSW()
bindOfflineToSession()

// the first frame is in the person's language: its catalog comes first
// (the boot script preloads it beside the bundle)
const render = () =>
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <App />
    </StrictMode>,
  )
// Without a catalog there is not a word to show: a plain note in both
// languages instead of raw keys (the one text outside the catalogs).
const noStrings = () => {
  const root = document.getElementById('root')!
  root.setAttribute('role', 'alert')
  root.style.cssText = 'display:grid;place-content:center;gap:16px;min-height:100dvh;padding:24px;text-align:center;font:var(--t-body)'
  const p = document.createElement('p')
  p.textContent = 'Не удалось загрузить приложение. · The app could not be loaded.'
  const b = document.createElement('button')
  b.type = 'button'
  b.textContent = 'Перезагрузить · Reload'
  b.style.cssText =
    'justify-self:center;height:var(--control-h-md);padding:0 var(--s-5);border:0;border-radius:var(--r-pill);background:var(--label);color:var(--label-inverse);font:var(--t-headline)'
  b.onclick = () => window.location.reload()
  root.replaceChildren(p, b)
}
void loadCatalogAtBoot(getLanguage()).then((ok) => (ok ? render() : noStrings()))
