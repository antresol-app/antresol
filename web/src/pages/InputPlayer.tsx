// /play/input/:id — a video an addon serves (an input it registered for
// this profile), in the core player. The server decides direct play or HLS
// (POST /api/play/input/{id}, 202 while it probes the file); until the
// first frames arrive the viewer sees the addon's own `playerNotes`, else
// «Загружается…»; a note's `actions` sit in the player's top bar and its
// `useProgress` tells the player how far the source has got (it keeps
// waiting while that grows). The position goes to the input's progress route (which
// saves the library item's progress when the input names one); the track
// choices are remembered for that item as for a library file.
import { APP_NAME } from '../brand'
import { createElement, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { Contribution } from '../addons/registry'
import { useLocation, useNavigate, useParams } from 'react-router'
import { ApiError, errorText } from '../api/client'
import { getInput, inputPlay, saveInputProgress } from '../api/inputs'
import type { MediaFile, PlayRequest, PlayerInput } from '../api/types'
import { AddonBoundary, useAddonSlot } from '../addons/slots'
import { Icon } from '../components/Icon'
import { Button } from '../ui/Button'
import { Spinner } from '../ui/Spinner'
import { VideoPlayer } from '../player/VideoPlayer'
import { audio } from '../player/audio/engine'
import { useT } from '../i18n'
import styles from './Player.module.css'

// the error, not its text: the text follows a language switch
type State = { status: 'loading' } | { status: 'gone' } | { status: 'error'; error: unknown } | { status: 'ready'; input: PlayerInput }

/** «Show/Show.S01E01.mkv» → «Show.S01E01». */
export function inputTitle(name: string): string {
  const file = name.split('/').pop() ?? ''
  const dot = file.lastIndexOf('.')
  return dot > 0 ? file.slice(0, dot) : file
}

/**
 * Calls a note's `useProgress` (a hook: in a component of its own, so one
 * that throws costs only itself) and keeps the answer where the player reads it.
 */
function NoteProgress({ note, input, into, at }: { note: Contribution<'playerNotes'>; input: PlayerInput; into: Map<number, number>; at: number }) {
  const n = note.useProgress!(input)
  useLayoutEffect(() => {
    into.set(at, n)
  })
  useEffect(() => () => void into.delete(at), [into, at])
  return null
}

export function InputPlayer() {
  const t = useT()
  // Video and music never play over each other (as in the other players):
  // a layout effect runs before the VideoPlayer's own Media Session effect.
  useLayoutEffect(() => audio.yieldSession(), [])
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const location = useLocation()
  const [state, setState] = useState<State>({ status: 'loading' })
  const gone = useRef(false)
  useEffect(() => {
    gone.current = false
    return () => void (gone.current = true)
  }, [])

  useEffect(() => {
    const ac = new AbortController()
    setState({ status: 'loading' })
    getInput(id, ac.signal).then(
      (input) => !ac.signal.aborted && setState({ status: 'ready', input }),
      (e: unknown) => {
        if (ac.signal.aborted) return
        if (e instanceof ApiError && e.status === 404) setState({ status: 'gone' })
        else setState({ status: 'error', error: e })
      },
    )
    return () => ac.abort()
  }, [id]) // not t: a language switch must not open the input again

  const input = state.status === 'ready' ? state.input : undefined
  const title = (input && inputTitle(input.name)) || t('player.input.title')
  useEffect(() => {
    document.title = `${title} — ${APP_NAME}`
  }, [title])

  const close = useCallback(() => {
    if (location.key !== 'default' && window.history.length > 1) navigate(-1)
    else navigate('/', { replace: true })
  }, [location.key, navigate])

  // Stable: a new start, saver or parts array would restart playback or save again.
  const start = useCallback((_f: MediaFile, body: PlayRequest) => inputPlay(id, body, () => gone.current), [id])
  const saveProgress = useCallback((p: number, d: number, k: boolean) => saveInputProgress(id, p, d, k), [id])
  const name = input?.name ?? ''
  const parts = useMemo<MediaFile[]>(() => [{ id: 0, name, size: 0, audio: [], subs: [] }], [name])

  // the notes of the addon that registered the input (admin-only ones for admins, none for kids)
  const slot = useAddonSlot('playerNotes')
  const addonId = input?.addonId
  const notes = useMemo(() => slot.filter((n) => n.addonId === addonId), [slot, addonId])
  // memoised: a new element tree would re-render the player's waiting note for nothing
  const waitingNote = useMemo(
    () =>
      input && notes.length > 0
        ? notes.map((n, i) => (
            // a note that throws renders nothing; only the first one's place falls back to the plain «Загружается…»
            <AddonBoundary key={`${n.addonId}-${i}`} addonId={n.addonId} fallback={i === 0 ? t('player.input.loading') : null}>
              {createElement(n.element, { input })}
            </AddonBoundary>
          ))
        : t('player.input.loading'),
    [input, notes, t],
  )

  // the notes' actions: the player's top bar (each in its own boundary)
  const topActions = useMemo(() => {
    const own = notes.filter((n) => n.actions)
    if (!input || own.length === 0) return undefined
    return own.map((n, i) => (
      <AddonBoundary key={`${n.addonId}-${i}`} addonId={n.addonId} fallback={null}>
        {createElement(n.actions!, { input })}
      </AddonBoundary>
    ))
  }, [input, notes])

  // the notes' source progress, by note: the player reads the sum
  const progress = useRef(new Map<number, number>()).current
  const reporting = notes.some((n) => n.useProgress)
  const sourceProgress = useCallback(() => {
    let sum = 0
    for (const v of progress.values()) if (Number.isFinite(v)) sum += v
    return sum
  }, [progress])

  if (state.status === 'loading') {
    return (
      <div className={styles.screen} aria-busy="true" aria-label={t('common.loading')}>
        <Spinner track className={styles.spinner} />
      </div>
    )
  }
  if (state.status === 'gone' || state.status === 'error') {
    return (
      <div className={styles.screen} role="alert">
        <Icon name="warning" size={40} strokeWidth={1.6} />
        <h1>{state.status === 'gone' ? t('player.input.gone') : t('player.page.openFailed')}</h1>
        {state.status === 'error' && <p>{errorText(state.error, t('player.page.openFailedDot'))}</p>}
        <Button className={styles.button} onClick={close}>
          {t('common.back')}
        </Button>
      </div>
    )
  }

  const { input: ready } = state

  return (
    <>
      {notes.map((n, i) =>
        n.useProgress ? (
          <AddonBoundary key={`${ready.id}-${n.addonId}-${i}`} addonId={n.addonId} fallback={null}>
            <NoteProgress note={n} input={ready} into={progress} at={i} />
          </AddonBoundary>
        ) : null,
      )}
      <VideoPlayer
        key={ready.id}
        itemId={ready.itemId}
        title={title}
        parts={parts}
        startSec={ready.startSec}
        onClose={close}
        start={start}
        saveProgress={saveProgress}
        waitingNote={waitingNote}
        slowSource={ready.slow}
        sourceProgress={reporting ? sourceProgress : undefined}
        topActions={topActions}
      />
    </>
  )
}
