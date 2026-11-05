// «Найти субтитры…» (admin): an OpenSubtitles search for one video file.
// Russian or English; the best results first («Совпадает с файлом» = made
// for exactly this file). «Скачать» saves the subtitles next to the video;
// the player then lists the new track and turns it on.
import { Fragment, useEffect, useState } from 'react'
import { ApiError, downloadSubtitle, searchSubtitles } from '../api/client'
import type { SubtitleResult } from '../api/types'
import { Button } from '../ui/Button'
import { Icon } from '../components/Icon'
import { SegmentedControl } from '../ui/SegmentedControl'
import { Sheet } from '../ui/Sheet'
import { locale, t, useT } from '../i18n'
import styles from './SubtitleSearchSheet.module.css'

export type SubtitleLang = 'ru' | 'en'

export const notConfiguredMessage = () => t('player.subs.notConfigured')
export const tracksNotRefreshed = () => t('player.subs.tracksNotRefreshed')

type State =
  | { status: 'loading' }
  | { status: 'error'; message: string; notConfigured: boolean }
  | { status: 'ready'; results: SubtitleResult[] }

export interface SubtitleSearchSheetProps {
  fileId: number
  onClose: () => void
  /**
   * A subtitle was saved for the file: the caller reloads its tracks. A
   * rejected promise keeps the sheet open with its message.
   */
  onDownloaded: (lang: SubtitleLang) => void | Promise<void>
  initialLang?: SubtitleLang
  /** Mount point (the player's root, so it shows in full screen). */
  container?: Element | null
}

/** A release name with line-break chances after «.», «-», «_» (no breaks mid-word). */
function breakable(name: string) {
  return name.split(/(?<=[._-])/).map((part, i) => (
    <Fragment key={i}>
      {i > 0 && <wbr />}
      {part}
    </Fragment>
  ))
}

export function SubtitleSearchSheet({ fileId, onClose, onDownloaded, initialLang = 'ru', container }: SubtitleSearchSheetProps) {
  const t = useT()
  const [lang, setLang] = useState<SubtitleLang>(initialLang)
  const [state, setState] = useState<State>({ status: 'loading' })
  const [busy, setBusy] = useState<number | null>(null)
  const [done, setDone] = useState<Set<number>>(new Set())
  const [problem, setProblem] = useState('')

  useEffect(() => {
    const ac = new AbortController()
    setState({ status: 'loading' })
    setProblem('')
    searchSubtitles(fileId, lang, ac.signal).then(
      (results) => setState({ status: 'ready', results }),
      (e: unknown) => {
        if (ac.signal.aborted) return
        const notConfigured = e instanceof ApiError && e.status === 503
        setState({
          status: 'error',
          notConfigured,
          message: notConfigured ? notConfiguredMessage() : e instanceof ApiError ? e.message : t('player.subs.searchFailed'),
        })
      },
    )
    return () => ac.abort()
  }, [fileId, lang])

  const download = async (r: SubtitleResult) => {
    setBusy(r.subtitleId)
    setProblem('')
    try {
      await downloadSubtitle(fileId, r.subtitleId, lang)
    } catch (e) {
      setProblem(e instanceof ApiError ? e.message : t('player.subs.downloadFailed'))
      setBusy(null)
      return
    }
    setDone((s) => new Set(s).add(r.subtitleId))
    try {
      await onDownloaded(lang)
    } catch (e) {
      setProblem(e instanceof Error && e.message ? e.message : tracksNotRefreshed())
    } finally {
      setBusy(null)
    }
  }

  return (
    <Sheet
      title={t('player.subs.title')}
      message={t('player.subs.message')}
      role="dialog"
      size="large"
      dismissOnOutside
      container={container}
      onCancel={onClose}
    >
      <div className={styles.langs}>
        <SegmentedControl
          label={t('player.subs.language')}
          segments={[
            { value: 'ru', label: t('player.subs.ru') },
            { value: 'en', label: t('player.subs.en') },
          ]}
          value={lang}
          onChange={(v) => setLang(v)}
        />
      </div>
      {problem && (
        <p className={styles.problem} role="alert">
          {problem}
        </p>
      )}
      {state.status === 'loading' && (
        <p className={styles.state} role="status">
          <span className={styles.spinner} aria-hidden /> {t('player.subs.searching')}
        </p>
      )}
      {state.status === 'error' && (
        <div className={styles.empty} role={state.notConfigured ? 'status' : 'alert'}>
          <Icon name={state.notConfigured ? 'captions' : 'cloud'} size={36} strokeWidth={1.5} />
          <p>{state.message}</p>
        </div>
      )}
      {state.status === 'ready' && state.results.length === 0 && (
        <div className={styles.empty} role="status">
          <Icon name="captions" size={36} strokeWidth={1.5} />
          <p>{lang === 'ru' ? t('player.subs.noneRu') : t('player.subs.noneEn')}</p>
        </div>
      )}
      {state.status === 'ready' && state.results.length > 0 && (
        <ul className={styles.list} aria-label={t('player.subs.results')}>
          {state.results.map((r) => {
            const saved = done.has(r.subtitleId)
            return (
              <li key={r.subtitleId} className={styles.row}>
                <div className={styles.text}>
                  <p className={styles.release} title={r.release}>
                    {r.release ? breakable(r.release) : t('common.untitled')}
                  </p>
                  <p className={styles.meta}>
                    {r.hashMatch && (
                      <span className={styles.match}>
                        <Icon name="checkCircle" size={14} strokeWidth={2.2} />
                        {t('player.subs.hashMatch')}
                      </span>
                    )}
                    <span>{t('player.subs.downloads', { count: r.downloads, n: r.downloads.toLocaleString(locale()) })}</span>
                  </p>
                </div>
                {saved ? (
                  <span className={styles.saved} role="status">
                    <Icon name="check" size={16} strokeWidth={2.4} /> {t('player.subs.saved')}
                  </span>
                ) : (
                  <Button
                    size="small"
                    variant="secondary"
                    icon="download"
                    aria-label={t('player.subs.downloadAria', { name: r.release || t('player.subs.fallbackName') })}
                    loading={busy === r.subtitleId}
                    disabled={busy !== null}
                    onClick={() => void download(r)}
                  >
                    {t('player.subs.download')}
                  </Button>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </Sheet>
  )
}
