import { useEffect, useRef, useState } from 'react'
import { t } from '../i18n'

interface Props {
  port: number
  device: string
  live: boolean
  disabled?: boolean
  invalid?: boolean
  /** Called with the raw text on Enter; resolve true to leave edit mode. */
  onSave: (text: string) => Promise<boolean>
  /** Esc or leaving edit mode without saving. */
  onCancel: () => void
}

/** The local port: a number that turns into an input on click (Enter saves, Esc cancels). */
export default function PortField({ port, device, live, disabled, invalid, onSave, onCancel }: Props) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)
  const saving = useRef(false)
  const wasEditing = useRef(false)
  const chipRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (editing) inputRef.current?.select()
    else if (wasEditing.current) chipRef.current?.focus()
    wasEditing.current = editing
  }, [editing])

  if (!editing) {
    return (
      <button
        ref={chipRef}
        type="button"
        className={live ? 'port live' : 'port'}
        title={`127.0.0.1:${port}`}
        aria-label={t('editPort', { name: device, port })}
        disabled={disabled}
        onClick={() => {
          setDraft(String(port))
          setEditing(true)
        }}
      >
        {port}
      </button>
    )
  }

  const save = async () => {
    if (saving.current) return
    saving.current = true
    try {
      if (await onSave(draft.trim())) setEditing(false)
    } finally {
      saving.current = false
    }
  }
  const cancel = () => {
    setEditing(false)
    onCancel()
  }

  return (
    <input
      ref={inputRef}
      className={invalid ? 'inp num bad' : 'inp num'}
      inputMode="numeric"
      aria-label={t('portOf', { name: device })}
      aria-invalid={invalid || undefined}
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onKeyDown={(e) => {
        if (e.nativeEvent.isComposing) return
        if (e.key === 'Enter') {
          e.preventDefault()
          void save()
        } else if (e.key === 'Escape') {
          e.preventDefault()
          e.stopPropagation()
          cancel()
        }
      }}
      onBlur={cancel}
    />
  )
}
