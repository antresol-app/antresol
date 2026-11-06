// Entry of the boot script (see ./early.ts): an input of its own in
// vite.config.ts, put into index.html before the app's bundle.
import { preloadCatalog, startEarlyMe } from './early'

startEarlyMe()
preloadCatalog()
