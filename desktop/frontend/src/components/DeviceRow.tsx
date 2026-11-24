import { useState } from 'react'
import { RemoveDevice, SetForward, SetPort, errorCode, errorDetail, type DeviceView, type ServerView } from '../api'
import { getLang, t } from '../i18n'
import { osLabel, since } from '../format'
import { useNow } from '../useNow'
import Switch from './Switch'
import PortField from './PortField'
import { CopyIcon } from './icons'
import Dialog from './Dialog'

interface Props {
  server: ServerView
  device: DeviceView
  disabled?: boolean
  onCommands: (device: string) => void
}

/** Local date and time of an RFC 3339 timestamp; "" when missing. */
function when(iso: string | undefined): string {
  if (!iso) return ''
  const at = Date.parse(iso)
  if (Number.isNaN(at) || at <= 0) return ''
  return new Date(at).toLocaleString(getLang() === 'zh' ? 'zh-CN' : 'en')
}

/** Sentence for a device.err code from the state; "" for none/unknown. */
function stateError(code: string, port: number): string {
  if (code === 'port_busy') return t('portBusy', { port })
  if (code === 'port_invalid') return t('portInvalid')
  if (code === 'port_conflict') return t('portConflict', { port })
  return ''
}

export default function DeviceRow({ server, device, disabled, onCommands }: Props) {
  const [localErr, setLocalErr] = useState('')
  const [attempt, setAttempt] = useState(device.port)
  const [busy, setBusy] = useState(false)
  const [open, setOpen] = useState(false)
  const [confirmRemove, setConfirmRemove] = useState(false)
  const [removing, setRemoving] = useState(false)
  const now = useNow()

  const live = device.on && device.online && server.conn === 'ready'
  const sys = osLabel(device.os, device.os_version)
  let status: string
  if (device.online) {
    const up = since(device.started_at, now)
    status = up ? `${sys}  ${t('devOnline', { d: up })}` : sys
  } else if (device.on) {
    status = t('devOfflineKept')
  } else {
    const seen = since(device.last_seen, now)
    status = seen ? t('devOffline', { d: seen }) : t('devOfflineNoSeen')
  }

  const errText = localErr || stateError(device.err, device.err === 'port_conflict' ? attempt : device.port)

  const reject = (e: unknown, port: number) => {
    const code = errorCode(e)
    if (code === 'port_busy') setLocalErr(t('portBusy', { port }))
    else if (code === 'offline') setLocalErr(t('deviceOffline', { name: device.name }))
    else if (code === 'port_invalid') setLocalErr(t('portInvalid'))
    else if (code === 'port_conflict') setLocalErr(t('portConflict', { port }))
    else if (code === 'forward_on') setLocalErr(t('portLocked'))
    else setLocalErr(errorDetail(e))
  }

  const toggle = async (next: boolean) => {
    setLocalErr('')
    setBusy(true)
    try {
      await SetForward(server.name, device.name, next)
    } catch (e) {
      reject(e, device.port)
    } finally {
      setBusy(false)
    }
  }

  const savePort = async (text: string): Promise<boolean> => {
    const n = Number(text)
    if (!/^\d+$/.test(text) || n < 1024 || n > 65535) {
      setLocalErr(t('portInvalid'))
      return false
    }
    setLocalErr('')
    if (n === device.port) return true
    setAttempt(n)
    try {
      await SetPort(server.name, device.name, n)
      return true
    } catch (e) {
      reject(e, n)
      return false
    }
  }

  const remove = async () => {
    setConfirmRemove(false)
    setLocalErr('')
    setRemoving(true)
    try {
      await RemoveDevice(server.name, device.name)
    } catch (e) {
      const code = errorCode(e)
      if (code === 'online') setLocalErr(t('devRemoveOnline', { name: device.name }))
      else if (code === 'not_connected') setLocalErr(t('devRemoveNotConnected'))
      else setLocalErr(errorDetail(e))
    } finally {
      setRemoving(false)
    }
  }

  const unknown = t('devUnknown')
  const system = [device.os ? osLabel(device.os, device.os_version) : '', device.arch].filter(Boolean).join(' · ')
  const details: [string, string, boolean?][] = [
    [t('devHost'), device.hostname || unknown],
    [t('devSystem'), system || unknown],
    [t('devTunnel'), device.tunnel_version || unknown],
    device.online
      ? [t('devStarted'), when(device.started_at) || unknown]
      : [t('devLastSeen'), when(device.last_seen) || unknown],
    [t('devId'), device.device_id || unknown, true],
  ]
  const detailId = `dev-detail-${server.name}-${device.name}`

  return (
    <div className={device.online ? 'item' : 'item offline'}>
      <div className="drow">
        <span className={device.online ? 'dot on' : 'dot off'} />
        <div className="grow">
          <button
            type="button"
            className={open ? 'name dname open' : 'name dname'}
            aria-expanded={open}
            aria-controls={detailId}
            title={t('devDetails', { name: device.name })}
            onClick={() => setOpen((o) => !o)}
          >
            {device.name}
            <svg className="chev" width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
              <path d="M3 2l3 3-3 3" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          </button>
          <div className="meta" title={status}>
            {status}
          </div>
        </div>
        <div className="conn">
          <PortField
            port={device.port}
            device={device.name}
            live={live}
            disabled={disabled}
            locked={device.on}
            invalid={!!errText}
            onSave={savePort}
            onCancel={() => setLocalErr('')}
          />
          <Switch
            checked={device.on}
            label={t('forwardOf', { name: device.name })}
            disabled={disabled || busy || (!device.online && !device.on)}
            onChange={toggle}
          />
        </div>
        <span className="vsep" aria-hidden="true" />
        <button
          type="button"
          className="copy"
          title={t('showCmds')}
          aria-label={t('cmdsTitle', { name: device.name })}
          disabled={disabled}
          onClick={() => onCommands(device.name)}
        >
          <CopyIcon />
        </button>
      </div>
      {errText && (
        <div className="perr" role="alert">
          {errText}
        </div>
      )}
      {open && (
        <div className="ddetail" id={detailId}>
          <dl>
            {details.map(([k, v, mono]) => (
              <div key={k}>
                <dt>{k}</dt>
                <dd className={mono ? 'mono' : undefined}>{v}</dd>
              </div>
            ))}
          </dl>
          {!device.online && (
            <button
              type="button"
              className="lb danger"
              disabled={disabled || removing || server.conn !== 'ready'}
              title={server.conn !== 'ready' ? t('devRemoveNotConnected') : undefined}
              onClick={() => setConfirmRemove(true)}
            >
              {removing && <i className="spinner" />}
              {t('devRemove')}
            </button>
          )}
        </div>
      )}
      {confirmRemove && (
        <Dialog
          title={t('devRemoveTitle', { name: device.name })}
          onCancel={() => setConfirmRemove(false)}
          footer={
            <>
              <button type="button" className="btn plain" onClick={() => setConfirmRemove(false)}>
                {t('cancel')}
              </button>
              <button type="button" className="btn dangerfill" onClick={() => void remove()}>
                {t('del')}
              </button>
            </>
          }
        >
          <p className="hint">{t('devRemoveBody')}</p>
        </Dialog>
      )}
    </div>
  )
}
