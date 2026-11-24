import { useRef, useState } from 'react'
import { DeleteServer, errorCode, errorMessage } from '../api'
import { t } from '../i18n'
import Dialog from './Dialog'

interface Props {
  name: string
  /** Forwards currently on for this server. */
  forwards: number
  onClose: () => void
}

export default function ConfirmDeleteDialog({ name, forwards, onClose }: Props) {
  const cancelRef = useRef<HTMLButtonElement>(null)
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState('')

  async function remove() {
    if (busy) return
    setBusy(true)
    setNote('')
    try {
      await DeleteServer(name)
      onClose()
    } catch (e) {
      setNote(errorCode(e) === 'cancelled' ? t('deleteCancelledNote') : errorMessage(e))
      setBusy(false)
    }
  }

  return (
    <Dialog
      title={t('deleteTitle')}
      onCancel={() => !busy && onClose()}
      initialFocus={cancelRef}
      footer={
        <>
          <button ref={cancelRef} type="button" className="btn plain" disabled={busy} onClick={onClose}>
            {t('cancel')}
          </button>
          <button type="button" className="btn dangerfill" disabled={busy} onClick={() => void remove()}>
            {t('del')}
          </button>
        </>
      }
    >
      <p className="confirm-text">{t('deleteConfirm', { name, n: forwards })}</p>
      {note && (
        <div className="errbox" role="alert">
          {note}
        </div>
      )}
    </Dialog>
  )
}
