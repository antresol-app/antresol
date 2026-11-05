// «О сервере» (profile menu): the server's version, its licence and texts,
// the source offer (AGPL §13; no link when the server gives none) and the
// TMDB notice its terms ask for.
import { APP_NAME } from '../brand'
import { useEffect, useState } from 'react'
import type { About } from '../api/types'
import { useT } from '../i18n'
import { loadAbout, sourceLink } from '../state/about'
import { ButtonAnchor } from '../ui/Button'
import { GroupedSection, ListRow } from '../ui/List'
import { Sheet } from '../ui/Sheet'
import { LogoMark } from './Logo'
import styles from './AboutSheet.module.css'

/** «0.4.0 (1a2b3c4)»: a commit is cut to the length git shows. */
export const versionLabel = (a: About) => (a.commit ? `${a.version} (${a.commit.slice(0, 7)})` : a.version)

export function AboutSheet({ onClose }: { onClose: () => void }) {
  const t = useT()
  const [about, setAbout] = useState<About | null | undefined>(undefined)
  useEffect(() => {
    let live = true
    void loadAbout().then((a) => live && setAbout(a))
    return () => {
      live = false
    }
  }, [])
  const source = sourceLink(about)
  const link = (href: string, label: string) => (
    <ButtonAnchor variant="plain" size="small" className={styles.rowLink} href={href} target="_blank" rel="noopener noreferrer">
      {label}
    </ButtonAnchor>
  )

  return (
    <Sheet title={t('comp.about.title')} message={APP_NAME} role="dialog" size="wide" dismissOnOutside icon={<LogoMark size={52} />} onCancel={onClose}>
      <div className={styles.body} tabIndex={-1} data-autofocus>
        {about === undefined && <p className={styles.note}>{t('common.loadingEllipsis')}</p>}
        {about === null && (
          <p className={styles.note} role="alert">
            {t('comp.serverUnreachable')}
          </p>
        )}
        {about && (
          <GroupedSection>
            <ListRow label={t('comp.about.version')}>
              <span className={styles.value}>{versionLabel(about)}</span>
            </ListRow>
            <ListRow label={t('comp.about.license')}>
              <span className={styles.value}>{about.license}</span>
            </ListRow>
          </GroupedSection>
        )}
        <GroupedSection footer={t('comp.about.tmdb')}>
          {source && <ListRow label={link(source, t('comp.about.source'))} />}
          <ListRow label={link('/api/about/license', t('comp.about.licenseText'))} />
          <ListRow label={link('/api/about/third-party', t('comp.about.thirdParty'))} />
        </GroupedSection>
      </div>
    </Sheet>
  )
}
