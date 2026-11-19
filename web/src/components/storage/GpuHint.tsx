// «Видеокарту в контейнер не передали»: what to uncomment in compose.yml. Shown
// under the hardware status (the wizard, «Настройки сервера → Перекодирование»)
// only when the server found no GPU to use and none was switched off on purpose
// or failed its test encode. A GPU that failed its test encode gets its own
// notice instead: the server's reason (permission, driver, encoder…) with the
// fix for it and ffmpeg's own words.
import type { ReactNode } from 'react'
import type { HWProblem, TranscodingView } from '../../api/types'
import { useT } from '../../i18n'
import { Notice } from '../../ui/Notice'
import { CopyBlock } from './CopyBlock'
import styles from './Storage.module.css'

/**
 * A GPU's own test encode failed: the server's reason (problem), or for one
 * that gives none, a failed probe that is not the CPU's tone mapping (that
 * one says nothing about the GPU).
 */
export function gpuProbeFailed(s: TranscodingView['status']): boolean {
  return !!s.problem || s.probes.some((p) => !p.ok && !p.mode.startsWith('tonemap'))
}

/** The container sees no render node and nothing else worked: no GPU was passed. */
export function noGpuPassed(v: TranscodingView): boolean {
  const s = v.status
  return s.mode === 'none' && s.requested !== 'none' && !gpuProbeFailed(s) && v.devices.length === 0
}

// The public image joins the render node's group by itself (its entrypoint);
// group_add is for a container run as another user (compose user:).
const DEVICES = `devices:
  - /dev/dri:/dev/dri`

export function GpuHint({ view }: { view: TranscodingView }) {
  const t = useT()
  const problem = view.status.problem
  if (problem && view.status.requested !== 'none') return <ProblemHint p={problem} />
  if (!noGpuPassed(view)) return null
  return (
    <Notice tone="accent" className={styles.fix}>
      <p className={styles.fixText}>{t('storage.hw.none')}</p>
      <CopyBlock text={DEVICES} />
      <p className={styles.fixText}>{t('storage.hw.nvidia')}</p>
      <p className={styles.fixText}>{t('storage.hw.apply')}</p>
    </Notice>
  )
}

/** A GPU that failed its test encode: what to do about the reason, and ffmpeg's words. */
function ProblemHint({ p }: { p: HWProblem }) {
  const t = useT()
  const device = p.device ?? ''
  let fix: ReactNode
  let compose = false
  switch (p.kind) {
    case 'permission':
      compose = true
      fix =
        p.gid && p.gid > 0 ? (
          <>
            <p className={styles.fixText}>{t('storage.hw.fix.permission', { device, gid: p.gid })}</p>
            <CopyBlock text={`group_add:\n  - "${p.gid}"`} />
          </>
        ) : (
          <>
            <p className={styles.fixText}>{t('storage.hw.fix.permissionNoGid', { device })}</p>
            <CopyBlock text={`ls -ln ${device}`} />
          </>
        )
      break
    case 'noDevice':
      compose = true
      fix = (
        <>
          <p className={styles.fixText}>{t('storage.hw.fix.noDevice', { device })}</p>
          <CopyBlock text={DEVICES} />
        </>
      )
      break
    case 'driver':
      compose = true
      fix = (
        <>
          <p className={styles.fixText}>{t('storage.hw.fix.driver')}</p>
          <CopyBlock text={driverEnv(p.driver)} />
        </>
      )
      break
    case 'nvidia':
      compose = true
      fix = (
        <>
          <p className={styles.fixText}>{t('storage.hw.fix.nvidia')}</p>
          <CopyBlock text="docker run --rm --gpus all ubuntu nvidia-smi" />
        </>
      )
      break
    case 'encoder':
      fix = <p className={styles.fixText}>{t('storage.hw.fix.encoder', { encoder: p.encoder ?? '' })}</p>
      break
    case 'timeout':
      fix = <p className={styles.fixText}>{t('storage.hw.fix.timeout')}</p>
      break
    default:
      fix = <p className={styles.fixText}>{t('storage.hw.fix.unknown')}</p>
  }
  return (
    <Notice tone="warning" title={t('storage.hw.fix.title')} className={styles.fix}>
      {fix}
      {compose && <p className={styles.fixText}>{t('storage.hw.apply')}</p>}
      {p.detail && (
        <>
          <p className={styles.fixText}>{t('storage.hw.fix.detail')}</p>
          <CopyBlock text={p.detail} />
        </>
      )}
    </Notice>
  )
}

// LIBVA_DRIVER_NAME for .env: the vendor's driver when the server knows it
function driverEnv(driver?: string): string {
  if (driver?.startsWith('radeonsi')) return 'LIBVA_DRIVER_NAME=radeonsi'
  if (driver?.startsWith('iHD')) return 'LIBVA_DRIVER_NAME=iHD\n# Intel before Broadwell (2014): LIBVA_DRIVER_NAME=i965'
  return 'LIBVA_DRIVER_NAME=iHD   # Intel; i965 before 2014; radeonsi for AMD'
}
