import { useState } from 'react'
import { SetConnected, Select, type AppState, type ServerView } from '../api'
import { actionError } from '../format'
import { t } from '../i18n'
import { EditIcon, GearIcon, PlusIcon, PowerIcon, TrashIcon } from './icons'

interface Props {
  state: AppState
  onAdd: () => void
  onEdit: (name: string) => void
  onDelete: (name: string) => void
  onSettings: () => void
}

type Dot = 'on' | 'err' | 'idle'

function rowStatus(s: ServerView): { dot: Dot; unreachable: boolean } {
  if (s.conn === 'disconnected') return { dot: 'idle', unreachable: false }
  if (s.conn === 'waiting' || s.conn === 'failed') return { dot: 'err', unreachable: true }
  if (s.share.on && (s.share.phase === 'failed' || s.share.phase === 'not_running')) return { dot: 'err', unreachable: false }
  if (s.conn === 'connecting') return { dot: 'idle', unreachable: false }
  return { dot: 'on', unreachable: false }
}

function report(e: unknown) {
  console.error(e)
}

export default function Sidebar({ state, onAdd, onEdit, onDelete, onSettings }: Props) {
  const servers = state.servers ?? []
  // last failed connect/disconnect, shown under that server's row
  const [actErr, setActErr] = useState<{ server: string; text: string } | null>(null)
  const toggleConnected = (name: string, on: boolean) => {
    setActErr(null)
    SetConnected(name, on).catch((e) => setActErr({ server: name, text: actionError(e) }))
  }
  return (
    <aside className="sidebar">
      {state.platform === 'darwin' && <div className="side-top" />}
      <div className="side-title">{t('servers')}</div>
      <div className="servers">
        {servers.map((s) => {
          const { dot, unreachable } = rowStatus(s)
          const disconnected = s.conn === 'disconnected'
          const toggle = disconnected ? t('connect') : t('disconnect')
          return (
            <div key={s.name} className={s.name === state.selected ? 'srv sel' : 'srv'}>
              <button
                type="button"
                className="srv-main"
                aria-current={s.name === state.selected ? 'true' : undefined}
                onClick={() => Select(s.name).catch(report)}
              >
                <span className={`dot ${dot}`} />
                <div className="grow">
                  <div className="n">{s.name}</div>
                  <div className={unreachable ? 'm err' : 'm'}>
                    {disconnected
                      ? t('paused')
                      : unreachable
                        ? t('unreachable')
                        : s.conn === 'connecting'
                          ? t('connecting')
                          : t('serverSummary', { connected: s.connected, online: s.online })}
                  </div>
                  {actErr?.server === s.name && (
                    <div className="m err" role="alert">
                      {actErr.text}
                    </div>
                  )}
                </div>
              </button>
              <span className="srv-acts">
                <button
                  type="button"
                  className="sa"
                  title={t('edit')}
                  aria-label={`${t('edit')} ${s.name}`}
                  onClick={() => onEdit(s.name)}
                >
                  <EditIcon />
                </button>
                <button
                  type="button"
                  className={disconnected ? 'sa' : 'sa on'}
                  title={toggle}
                  aria-label={`${toggle} ${s.name}`}
                  onClick={() => toggleConnected(s.name, disconnected)}
                >
                  <PowerIcon />
                </button>
                <button
                  type="button"
                  className="sa danger"
                  title={t('del')}
                  aria-label={`${t('del')} ${s.name}`}
                  onClick={() => onDelete(s.name)}
                >
                  <TrashIcon />
                </button>
              </span>
            </div>
          )
        })}
      </div>
      <div className="side-bottom">
        <button type="button" className="addbtn" onClick={onAdd}>
          <PlusIcon />
          {t('addServer')}
        </button>
        <button type="button" className="icon" aria-label={t('settings')} title={t('settings')} onClick={onSettings}>
          <GearIcon />
        </button>
      </div>
    </aside>
  )
}
