import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './design/tokens.css'
import './design/glass.css'
import { App } from './app/App'
import { registerSW } from './offline/register'
import { bindOfflineToSession } from './offline/store'

registerSW()
bindOfflineToSession()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
