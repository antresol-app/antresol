// Choosing a profile's photo: a hidden file input (opened by a button of the
// caller's), the checks on the file, and the frame (PhotoCropSheet) it opens in.
// AvatarEditor (an admin's form, applied with Save) and ProfilePhotoRow (a
// profile's own, applied at once) both use it; what happens to the framed
// square is theirs.
import { useRef, useState, type ChangeEvent, type ReactNode } from 'react'
import { useT } from '../i18n'
import { PhotoCropSheet } from './PhotoCropSheet'
import { SOURCE_MAX_BYTES } from './photoCrop'

export interface PhotoPick {
  /** Opens the system's file picker. */
  open: () => void
  /** Why the last file was refused ('' none): show it near the button. */
  error: string
  /** Render once: the input and the frame. */
  element: ReactNode
}

/** `onPhoto` gets the framed square, as a JPEG. */
export function usePhotoPick(onPhoto: (photo: Blob) => void): PhotoPick {
  const t = useT()
  const input = useRef<HTMLInputElement>(null)
  const [picked, setPicked] = useState<File | null>(null)
  const [error, setError] = useState('')

  const pick = (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    e.target.value = '' // the same file may be chosen again
    if (!file) return
    if (file.type && !file.type.startsWith('image/')) return setError(t('comp.photo.notImage'))
    if (file.size > SOURCE_MAX_BYTES) return setError(t('comp.photo.tooBig', { mb: SOURCE_MAX_BYTES / 1024 / 1024 }))
    setError('')
    setPicked(file)
  }

  return {
    open: () => input.current?.click(),
    error,
    element: (
      <>
        <input ref={input} type="file" accept="image/*" hidden onChange={pick} data-testid="photo-input" aria-label={t('comp.photo.label')} />
        {picked && (
          <PhotoCropSheet
            file={picked}
            onCancel={() => setPicked(null)}
            onDone={(b) => {
              setPicked(null)
              onPhoto(b)
            }}
          />
        )}
      </>
    ),
  }
}
