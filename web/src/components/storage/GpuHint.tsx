// «Видеокарту в контейнер не передали»: what to uncomment in compose.yml. Shown
// under the hardware status (the wizard, «Настройки сервера → Перекодирование»)
// only when the server found no GPU to use and none was switched off on purpose
// or failed its test encode.
import type { TranscodingView } from '../../api/types'
import { useT } from '../../i18n'
import { Notice } from '../../ui/Notice'
import { CopyBlock } from './CopyBlock'
import styles from './Storage.module.css'

/** The container sees no render node and nothing else worked: no GPU was passed. */
export function noGpuPassed(v: TranscodingView): boolean {
  const s = v.status
  return s.mode === 'none' && s.requested !== 'none' && !s.probes.some((p) => !p.ok) && v.devices.length === 0
}

const COMPOSE = `devices:
  - /dev/dri:/dev/dri
group_add:
  - "992"`

export function GpuHint({ view }: { view: TranscodingView }) {
  const t = useT()
  if (!noGpuPassed(view)) return null
  return (
    <Notice tone="accent" className={styles.fix}>
      <p className={styles.fixText}>{t('storage.hw.none')}</p>
      <CopyBlock text={COMPOSE} />
      <p className={styles.fixText}>{t('storage.hw.group')}</p>
      <CopyBlock text="getent group render | cut -d: -f3" />
      <p className={styles.fixText}>{t('storage.hw.nvidia')}</p>
      <p className={styles.fixText}>{t('storage.hw.apply')}</p>
    </Notice>
  )
}
