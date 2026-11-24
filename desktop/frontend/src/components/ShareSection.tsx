import { Suspense, lazy, useEffect, useRef, useState } from 'react'
import {
  RetryShare,
  SetShare,
  SetShareName,
  SetShareSystem,
  UpdateShare,
  errorCode,
  errorDetail,
  type ServerView,
  type Settings,
} from '../api'
import { t, type MsgKey } from '../i18n'
import Switch from './Switch'
import Dialog from './Dialog'
import Countdown from './Countdown'
import { plainText } from '../plainText'

interface Props {
  server: ServerView
  platform: string
  sshd: { running: boolean; port: number }
  settings: Settings
  disabled?: boolean
}

const SSHD_OFF: Record<string, MsgKey> = {
  darwin: 'sshdOffDarwin',
  windows: 'sshdOffWindows',
  linux: 'sshdOffLinux',
}

// xterm.js is large: load the viewer only when it is first opened.
const LogViewer = lazy(() => import('./LogViewer'))

type Note = { kind: 'text'; text: string }

export default function ShareSection({ server, platform, sshd, settings, disabled }: Props) {
  const { share } = server
  const [draft, setDraft] = useState(share.name)
  const focused = useRef(false)
  const [nameErr, setNameErr] = useState('')
  const [note, setNote] = useState<Note | null>(null)
  const [confirmVault, setConfirmVault] = useState(false)
  const [logOpen, setLogOpen] = useState(false)
  const attempted = useRef('')
  const noteTimer = useRef<ReturnType<typeof setTimeout>>(undefined)
  useEffect(() => () => clearTimeout(noteTimer.current), [])

  // follow the backend name unless the user is typing
  useEffect(() => {
    if (!focused.current) setDraft(share.name)
  }, [share.name])

  const locked = disabled || share.busy
  // a disabled input never fires blur; do not leave the focus flag stuck
  useEffect(() => {
    if (disabled) focused.current = false
  }, [disabled])
  const flash = (text: string) => {
    setNote({ kind: 'text', text })
    clearTimeout(noteTimer.current)
    noteTimer.current = setTimeout(() => setNote(null), 4000)
  }
  const sshdOff = () => t(SSHD_OFF[platform] ?? 'sshdOffLinux')
  const fail = (e: unknown) => {
    const code = errorCode(e)
    if (code === 'cancelled') flash(t('cancelledAuth'))
    else if (code === 'not_running') setNote({ kind: 'text', text: t('errNotRunning') })
    else if (code === 'no_sshd') setNote({ kind: 'text', text: sshdOff() })
    else if (code === 'name_taken') setNameErr(t('nameTaken', { name: attempted.current || share.name, server: server.name }))
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
    attempted.current = name
    void run(SetShareName(server.name, name))
  }

  const openLog = () => {
    setNote(null)
    setLogOpen(true)
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
          {share.on && !failed && (
            <button type="button" className="lb quiet" onClick={openLog}>
              {t('viewLogs')}
            </button>
          )}
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
              <button type="button" className="lb quiet" disabled={disabled} onClick={openLog}>
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
            {note.text}
          </div>
        )}

        <div className="sub">
          <div className="frow">
            <label className="lbl" htmlFor={`share-name-${server.name}`}>
              {t('deviceName')}
              <small>{t('deviceNameHint', { server: server.name })}</small>
            </label>
            <input {...plainText}
              id={`share-name-${server.name}`}
              className={nameMsg ? 'inp bad' : 'inp'}
              style={{ width: 180 }}
              value={draft}
              disabled={disabled}
              readOnly={share.busy}
              aria-busy={share.busy || undefined}
              aria-invalid={!!nameMsg || undefined}
              onFocus={() => (focused.current = true)}
              onChange={(e) => {
                setDraft(e.target.value)
                setNameErr('')
              }}
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
                  {sshdOff()}
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
      {logOpen && (
        <Suspense fallback={null}>
          <LogViewer server={server.name} settings={settings} onClose={() => setLogOpen(false)} />
        </Suspense>
      )}
    </div>
  )
}
