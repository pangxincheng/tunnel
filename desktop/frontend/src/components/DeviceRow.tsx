import { useState } from 'react'
import { SetForward, SetPort, errorCode, errorDetail, type DeviceView, type ServerView } from '../api'
import { t } from '../i18n'
import { osLabel, since } from '../format'
import { useNow } from '../useNow'
import Switch from './Switch'
import PortField from './PortField'
import { CopyIcon } from './icons'

interface Props {
  server: ServerView
  device: DeviceView
  disabled?: boolean
  onCommands: (device: string) => void
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

  return (
    <div className={device.online ? 'item' : 'item offline'}>
      <div className="drow">
        <span className={device.online ? 'dot on' : 'dot off'} />
        <div className="grow">
          <div className="name">{device.name}</div>
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
    </div>
  )
}
