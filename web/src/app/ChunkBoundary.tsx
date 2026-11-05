// Loading and failure states of a lazily loaded section: a calm spinner
// while the chunk downloads, and a friendly retry when it cannot be loaded
// (offline, or a new version replaced the old chunk files).
import { Component, Suspense, type ReactNode } from 'react'
import { Button } from '../ui/Button'
import { Icon } from '../components/Icon'
import styles from './ChunkBoundary.module.css'
import { useT } from '../i18n'

interface State {
  failed: boolean
}

class Boundary extends Component<{ children: ReactNode }, State> {
  state: State = { failed: false }
  static getDerivedStateFromError(): State {
    return { failed: true }
  }
  render() {
    if (!this.state.failed) return this.props.children
    return (
      <ChunkFailed />
    )
  }
}

function ChunkFailed() {
  const t = useT()
  return (
    <div className={styles.box} role="alert">
      <Icon name="cloud" size={44} strokeWidth={1.4} />
      <p className={styles.title}>{t('app.chunk.failed')}</p>
      <p className={styles.text}>{t('app.chunk.failedHint')}</p>
      <Button variant="secondary" onClick={() => window.location.reload()}>
        {t('common.retry')}
      </Button>
    </div>
  )
}

export function ChunkBoundary({ children }: { children: ReactNode }) {
  const t = useT()
  return (
    <Boundary>
      <Suspense
        fallback={
          <div className={styles.box} aria-busy="true" aria-label={t('common.loading')}>
            <span className={styles.spinner} />
          </div>
        }
      >
        {children}
      </Suspense>
    </Boundary>
  )
}
