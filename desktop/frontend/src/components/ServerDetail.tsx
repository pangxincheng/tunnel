import { useEffect, useState } from 'react'
import type { ServerView } from '../api'
import { t } from '../i18n'
import Banner from './Banner'
import ShareSection from './ShareSection'
import DeviceRow from './DeviceRow'
import CommandsDialog from './CommandsDialog'

interface Props {
  server: ServerView
  platform: string
  sshd: { running: boolean; port: number }
}

export default function ServerDetail({ server, platform, sshd }: Props) {
  const [cmdsFor, setCmdsFor] = useState<string | null>(null)
  const disabled = server.conn === 'disconnected'
  const stale = server.conn === 'waiting' || server.conn === 'failed'
  const devices = server.devices ?? []
  const cmdsDevice = cmdsFor ? devices.find((d) => d.name === cmdsFor) : undefined
  // forget the device once it is gone, so it cannot pop the dialog open when it returns
  useEffect(() => {
    if (cmdsFor && !cmdsDevice) setCmdsFor(null)
  }, [cmdsFor, cmdsDevice])

  return (
    <>
      <div className="main-top">
        <span className="t">{server.name}</span>
        <span className="h" title={`${server.user}@${server.host}`}>
          {server.user}@{server.host}
        </span>
      </div>
      <div className="content">
        <div className="col">
          <Banner server={server} />
          <div className={disabled ? 'body faded' : 'body'} aria-disabled={disabled || undefined}>
            <ShareSection server={server} platform={platform} sshd={sshd} disabled={disabled} />
            <div className="sec">
              <div className="sec-head">
                <span className="t">{t('connectOthers')}</span>
                <span className="s">{t('connectHint')}</span>
              </div>
              <div className={stale ? 'list stale' : 'list'}>
                {devices.map((d) => (
                  <DeviceRow key={d.name} server={server} device={d} disabled={disabled} onCommands={setCmdsFor} />
                ))}
              </div>
            </div>
          </div>
        </div>
      </div>
      {cmdsDevice && (
        <CommandsDialog
          name={cmdsDevice.name}
          port={cmdsDevice.port}
          forwardOn={cmdsDevice.on}
          onClose={() => setCmdsFor(null)}
        />
      )}
    </>
  )
}
