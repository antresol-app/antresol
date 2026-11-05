// «Исходный код · AGPL»: the source offer under the sign-in screens (AGPL
// §13). Nothing when the server offers no link (a build from source, the
// owner's own server).
import { ButtonAnchor } from '../ui/Button'
import { useT } from '../i18n'
import { sourceLink, useAbout } from '../state/about'
import styles from './SourceLink.module.css'

export function SourceLink() {
  const t = useT()
  const href = sourceLink(useAbout())
  if (!href) return null
  return (
    <p className={styles.source}>
      <ButtonAnchor variant="link" href={href} target="_blank" rel="noopener noreferrer">
        {t('comp.about.source')}
      </ButtonAnchor>
    </p>
  )
}
