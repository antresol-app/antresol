// Entry of the boot script (see ./early.ts): an input of its own in
// vite.config.ts, put into index.html before the app's bundle.
import { startEarlyMe } from './early'

startEarlyMe()
