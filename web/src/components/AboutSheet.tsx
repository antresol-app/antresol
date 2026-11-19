// «О сервере» (profile menu): the server's version, its licence and texts,
// the source offer (AGPL §13; no link when the server gives none), the
// address to open on the home network (antresol.local, when the server
// holds that name; why not, when it cannot) and the TMDB notice its terms
// ask for.
import { APP_NAME } from '../brand'
import { useEffect, useState } from 'react'
import { getLocalName, getTranscoding } from '../api/client'
import type { About, LocalName, TranscodingView } from '../api/types'
import { useT } from '../i18n'
import { loadAbout, sourceLink } from '../state/about'
import { ButtonAnchor } from '../ui/Button'
import { GroupedSection, ListRow } from '../ui/List'
import { Sheet } from '../ui/Sheet'
import { LogoMark } from './Logo'
import { statusLine } from '../pages/manage/TranscodingSettings'
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
  // the GPU line: admins only (the request fails for others, and the row stays out)
  const [tc, setTc] = useState<TranscodingView | null>(null)
  useEffect(() => {
    const ac = new AbortController()
    getTranscoding(ac.signal).then((v) => setTc(v?.status?.mode ? v : null), () => {})
    return () => ac.abort()
  }, [])
  // the home-network name: shown when there is one, or a reason worth acting on
  const [local, setLocal] = useState<LocalName | null>(null)
  useEffect(() => {
    const ac = new AbortController()
    getLocalName(ac.signal).then((v) => setLocal(v && typeof v.state === 'string' ? v : null), () => {})
    return () => ac.abort()
  }, [])
  // the address itself is the row's value; a reason is a sentence under the label
  const localURL = local?.state === 'ready' ? (local.url ?? '') : ''
  const localWhy =
    local?.state === 'probing'
      ? t('comp.about.localProbing')
      : local?.state === 'bridge'
        ? t('comp.about.localBridge')
        : local?.state === 'vm'
          ? t('comp.about.localVM')
          : local?.state === 'failed'
            ? t('comp.about.localFailed')
            : ''
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
            {tc && (
              <ListRow label={t('comp.about.video')} hint={statusLine(tc, t)} />
            )}
          </GroupedSection>
        )}
        {(localURL || localWhy) && (
          <GroupedSection footer={localURL ? t('comp.about.localHint') : undefined}>
            <ListRow stack label={t('comp.about.local')} hint={localWhy || undefined}>
              {localURL && <span className={styles.address}>{localURL}</span>}
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
