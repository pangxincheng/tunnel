import { useEffect, useRef, useState } from 'react'
import { t } from '../i18n'
import { commandsFor } from '../commands'
import Dialog from './Dialog'

interface Props {
  name: string
  port: number
  forwardOn: boolean
  onClose: () => void
}

/** Renders a command with its <placeholders> dimmed and <USERNAME> highlighted. */
function Cmd({ text }: { text: string }) {
  return (
    <code>
      {text.split(/(<[^>]+>)/).map((p, i) =>
        p.startsWith('<') ? (
          <span key={i} className={p === '<USERNAME>' ? 'ph user' : 'ph'}>
            {p}
          </span>
        ) : (
          p
        ),
      )}
    </code>
  )
}

export default function CommandsDialog({ name, port, forwardOn, onClose }: Props) {
  const doneRef = useRef<HTMLButtonElement>(null)
  const [copied, setCopied] = useState<number | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])

  const copy = (i: number, text: string) => {
    Promise.resolve(navigator.clipboard?.writeText(text))
      .then(() => {
        setCopied(i)
        clearTimeout(timer.current)
        timer.current = setTimeout(() => setCopied(null), 1500)
      })
      .catch((e) => console.error('clipboard write failed', e))
  }

  return (
    <Dialog
      title={t('cmdsTitle', { name })}
      subtitle={forwardOn ? t('cmdsOn', { port, name }) : t('cmdsOff')}
      wide
      onCancel={onClose}
      onSubmit={onClose}
      initialFocus={doneRef}
      footer={
        <button ref={doneRef} type="button" className="btn primary" onClick={onClose}>
          {t('done')}
        </button>
      }
    >
      <div className="list cmdlist">
        {commandsFor(port).map((c, i) => (
          <div className="c" key={c.key}>
            <span className="k">{t(c.key)}</span>
            <Cmd text={c.text} />
            <button type="button" className="lb" aria-label={`${t('copyCmd')} ${t(c.key)}`} onClick={() => copy(i, c.text)}>
              {copied === i ? t('copied') : t('copyCmd')}
            </button>
          </div>
        ))}
      </div>
      <p className="hint">{t('cmdsHint', { name })}</p>
    </Dialog>
  )
}
