// A command or a few lines of compose.yml to copy: the text as it is typed,
// one «Скопировать» beside it. The fixes the wizard shows are meant to be
// pasted, so the block is selectable and the button copies all of it.
import { useRef, useState } from 'react'
import { Button } from '../../ui/Button'
import { useT } from '../../i18n'
import styles from './Storage.module.css'

async function copyText(text: string, node: HTMLElement | null): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    // no clipboard API (plain http): select the text and copy the old way
    if (!node) return false
    const range = document.createRange()
    range.selectNodeContents(node)
    const sel = window.getSelection()
    sel?.removeAllRanges()
    sel?.addRange(range)
    try {
      return document.execCommand('copy')
    } catch {
      return false
    }
  }
}

/** `stacked`: the button under the text, whatever the width (a sheet's narrow column). */
export function CopyBlock({ text, label, stacked }: { text: string; label?: string; stacked?: boolean }) {
  const t = useT()
  const ref = useRef<HTMLPreElement>(null)
  const [copied, setCopied] = useState(false)
  return (
    <div className={[styles.copyBlock, stacked ? styles.copyStacked : ''].filter(Boolean).join(' ')}>
      <pre className={styles.code} ref={ref} tabIndex={0} aria-label={label}>
        {text}
      </pre>
      <Button
        size="small"
        variant="secondary"
        onClick={() => {
          void copyText(text, ref.current).then((ok) => {
            setCopied(ok)
            if (ok) window.setTimeout(() => setCopied(false), 2000)
          })
        }}
      >
        {copied ? t('storage.copied') : t('storage.copy')}
      </Button>
    </div>
  )
}
