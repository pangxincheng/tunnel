import { useEffect, useRef, useState } from 'react'
import {
  OpenLog,
  RetryShare,
  SetShare,
  SetShareName,
  SetShareSystem,
  UpdateShare,
  errorCode,
  errorDetail,
  type ServerView,
} from '../api'
import { t, type MsgKey } from '../i18n'
import Switch from './Switch'
import Dialog from './Dialog'
import Countdown from './Countdown'

interface Props {
  server: ServerView
  platform: string
  sshd: { running: boolean; port: number }
  disabled?: boolean
}

const SSHD_HINT: Record<string, MsgKey> = {
  darwin: 'sshdHintDarwin',
  windows: 'sshdHintWindows',
  linux: 'sshdHintLinux',
}

type Note = { kind: 'text'; text: string } | { kind: 'journal'; cmd: string }

export default function ShareSection({ server, platform, sshd, disabled }: Props) {
  const { share } = server
  const [draft, setDraft] = useState(share.name)
  const focused = useRef(false)
  const [nameErr, setNameErr] = useState('')
  const [note, setNote] = useState<Note | null>(null)
  const [confirmVault, setConfirmVault] = useState(false)
  const [copied, setCopied] = useState(false)
  const noteTimer = useRef<ReturnType<typeof setTimeout>>(undefined)
  useEffect(() => () => clearTimeout(noteTimer.current), [])

  // follow the backend name unless the user is typing
  useEffect(() => {
    if (!focused.current) setDraft(share.name)
  }, [share.name])

  const locked = disabled || share.busy
  const flash = (text: string) => {
    setNote({ kind: 'text', text })
    clearTimeout(noteTimer.current)
    noteTimer.current = setTimeout(() => setNote(null), 4000)
  }
  const sshdHint = () => t(SSHD_HINT[platform] ?? 'sshdHintLinux')
  const fail = (e: unknown) => {
    const code = errorCode(e)
    if (code === 'cancelled') flash(t('cancelledAuth'))
    else if (code === 'no_sshd') setNote({ kind: 'text', text: `${t('sshdOff')}${sshdHint()}` })
    else if (code === 'name_taken') setNameErr(t('nameTaken', { name: share.name, server: server.name }))
    else setNote({ kind: 'text', text: errorDetail(e) })
  }
  const run = async (p: Promise<unknown>) => {
    setNote(null)
    setNameErr('')
    try {
      await p
    } catch (e) {
      fail(e)
    }
  }

  const saveName = () => {
    const name = draft.trim()
    if (name === share.name) return
    void run(SetShareName(server.name, name))
  }

  const openLog = async () => {
    setNote(null)
    try {
      await OpenLog(server.name)
    } catch (e) {
      const code = errorCode(e)
      if (code === 'journal') setNote({ kind: 'journal', cmd: errorDetail(e) })
      else if (code === 'nolog') setNote({ kind: 'text', text: t('logMissing') })
      else setNote({ kind: 'text', text: errorDetail(e) })
    }
  }

  const copyCmd = (cmd: string) => {
    Promise.resolve(navigator.clipboard?.writeText(cmd))
      .then(() => {
        setCopied(true)
        setTimeout(() => setCopied(false), 1500)
      })
      .catch((e) => console.error('clipboard write failed', e))
  }

  const toggleSystem = (next: boolean) => {
    if (next && platform === 'darwin') setConfirmVault(true)
    else void run(SetShareSystem(server.name, next))
  }

  const pn = share.name_err
  const nameMsg =
    pn === 'taken'
      ? t('nameTaken', { name: share.name, server: server.name })
      : pn === 'invalid'
        ? t('nameInvalid')
        : nameErr
  const phase = share.phase
  const failed = share.on && (phase === 'failed' || phase === 'not_running')

  let dot = 'idle'
  let meta: React.ReactNode = t('notShared')
  if (share.busy) {
    meta = (
      <>
        <i className="spinner" />
        {t('shareBusy')}
      </>
    )
  } else if (share.on) {
    if (phase === 'ready') {
      dot = 'on'
      meta = t('shareReady')
    } else if (phase === 'failed') {
      dot = 'err'
      meta = t('shareFailed')
    } else if (phase === 'not_running') {
      dot = 'err'
      meta = t('shareNotRunning')
    } else if (phase === 'waiting' && share.retry_at) {
      meta = <Countdown at={share.retry_at} />
    } else if (phase === 'paused') {
      meta = t('paused')
    } else {
      meta = (
        <>
          <i className="spinner" />
          {t('shareConnecting')}
        </>
      )
    }
  }

  const sshdBlocked = !sshd.running && !share.on
  const showSystem = platform === 'darwin' || platform === 'linux'

  return (
    <div className="sec">
      <div className="sec-head">
        <span className="t">{t('shareThis')}</span>
        <span className="s">{t('shareHint')}</span>
      </div>
      <div className="list">
        <div className="drow">
          <span className={`dot ${dot}`} />
          <div className="grow">
            <div className="name">
              {share.name}
              <span className="tag">{t('thisPc')}</span>
            </div>
            <div className={failed ? 'meta err' : 'meta'}>
              {meta}
              {share.on && share.system && !share.busy && (
                <>
                  <span className="gap" />
                  {t('keepOn')}
                </>
              )}
            </div>
          </div>
          <Switch
            checked={share.on}
            label={t('shareThis')}
            disabled={locked || (!share.on && (!!share.name_err || sshdBlocked))}
            onChange={(on) => void run(SetShare(server.name, on))}
          />
        </div>

        {failed && (
          <div className="errbox">
            {share.err && <div>{share.err}</div>}
            <div className="acts">
              <button type="button" className="lb" disabled={locked} onClick={() => void run(RetryShare(server.name))}>
                {t('retry')}
              </button>
              <button type="button" className="lb quiet" disabled={disabled} onClick={() => void openLog()}>
                {t('viewLogs')}
              </button>
            </div>
          </div>
        )}

        {share.on && share.outdated && (
          <div className="errbox info">
            <div>{t('updateShareHint')}</div>
            <div className="acts">
              <button type="button" className="lb" disabled={locked} onClick={() => void run(UpdateShare(server.name))}>
                {t('updateShare')}
              </button>
            </div>
          </div>
        )}

        {note && (
          <div className="errbox" role="status">
            {note.kind === 'text' ? (
              note.text
            ) : (
              <>
                <div>{t('logJournal')}</div>
                <div className="cmdline">
                  <code className="mono">{note.cmd}</code>
                  <button type="button" className="lb" onClick={() => copyCmd(note.cmd)}>
                    {copied ? t('copied') : t('copyCmd')}
                  </button>
                </div>
              </>
            )}
          </div>
        )}

        <div className="sub">
          <div className="frow">
            <label className="lbl" htmlFor={`share-name-${server.name}`}>
              {t('deviceName')}
              <small>{t('deviceNameHint', { server: server.name })}</small>
            </label>
            <input
              id={`share-name-${server.name}`}
              className={nameMsg ? 'inp bad' : 'inp'}
              style={{ width: 180 }}
              value={draft}
              disabled={locked}
              aria-invalid={!!nameMsg || undefined}
              onFocus={() => (focused.current = true)}
              onChange={(e) => setDraft(e.target.value)}
              onBlur={() => {
                focused.current = false
                saveName()
              }}
              onKeyDown={(e) => {
                if (e.nativeEvent.isComposing) return
                if (e.key === 'Enter') {
                  e.preventDefault()
                  saveName()
                } else if (e.key === 'Escape') {
                  setDraft(share.name)
                }
              }}
            />
          </div>
          {nameMsg && (
            <div className="ferr" role="alert">
              {nameMsg}
            </div>
          )}
          {showSystem && (
            <div className="frow">
              <span className="lbl">
                {t('unattended')}
                <small>{t('unattendedHint')}</small>
              </span>
              <Switch checked={share.system} label={t('unattended')} disabled={locked} onChange={toggleSystem} />
            </div>
          )}
          <div className="check">
            {sshd.running ? (
              <>
                <span className="tick">✓</span>
                <span>{t('sshdOk', { port: sshd.port })}</span>
              </>
            ) : (
              <>
                <span className="dot err" />
                <span className="sshd-off">
                  {t('sshdOff')}
                  {sshdHint()}
                </span>
              </>
            )}
          </div>
        </div>
      </div>

      {confirmVault && (
        <Dialog
          title={t('fileVaultTitle')}
          onCancel={() => setConfirmVault(false)}
          onSubmit={() => {
            setConfirmVault(false)
            void run(SetShareSystem(server.name, true))
          }}
          footer={
            <>
              <button type="button" className="btn plain" onClick={() => setConfirmVault(false)}>
                {t('cancel')}
              </button>
              <button
                type="button"
                className="btn primary"
                onClick={() => {
                  setConfirmVault(false)
                  void run(SetShareSystem(server.name, true))
                }}
              >
                {t('continue')}
              </button>
            </>
          }
        >
          <p className="hint">{t('fileVaultBody')}</p>
        </Dialog>
      )}
    </div>
  )
}
