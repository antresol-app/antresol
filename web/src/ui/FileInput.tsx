// The file chooser's input. A button of the screen's own (a tile, a «Загрузить»)
// opens it with `ref.current.click()`; the browser's chooser is the control, so
// nothing of it is drawn: `hidden` unless the caller says otherwise (a field the
// page keeps in its layout passes `hidden={false}` and a class that hides it).
// `accept`, `multiple`, `onChange` and the rest are the input's own.
import { forwardRef, type InputHTMLAttributes } from 'react'

export type FileInputProps = Omit<InputHTMLAttributes<HTMLInputElement>, 'type'>

export const FileInput = forwardRef<HTMLInputElement, FileInputProps>(function FileInput({ hidden = true, ...rest }, ref) {
  return <input ref={ref} type="file" hidden={hidden} {...rest} />
})
