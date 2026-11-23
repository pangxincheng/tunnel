import { Retry, SetConnected, type ServerView } from '../api'
import { t } from '../i18n'
import { since } from '../format'
import { useNow } from '../useNow'
import Countdown from './Countdown'

/** Status banner for a server that is unreachable or disconnected; nothing otherwise. */
export default function Banner({ server }: { server: ServerView }) {
  const now = useNow()
  if (server.conn === 'disconnected') {
    return (
      <div className="banner paused" role="status">
        <span className="dot idle" />
        <div className="grow">
          <div>{t('bannerPaused', { server: server.name })}</div>
        </div>
        <button
          type="button"
          className="btn primary sm"
          onClick={() => SetConnected(server.name, true).catch((e) => console.error('SetConnected failed', e))}
        >
          {t('connect')}
        </button>
      </div>
    )
  }
  if (server.conn !== 'waiting' && server.conn !== 'failed') return null
  const ago = since(server.devices_at, now)
  return (
    <div className="banner" role="alert">
      <span className="dot err" />
      <div className="grow">
        <div>
          {server.conn_err
            ? t('bannerDown', { server: server.name, reason: server.conn_err })
            : t('bannerDownNoReason', { server: server.name })}
        </div>
        <div>
          {ago && `${t('bannerStale', { ago })} `}
          {server.conn === 'waiting' && server.retry_at && <Countdown at={server.retry_at} />}
        </div>
      </div>
      <button
        type="button"
        className="lb"
        onClick={() => Retry(server.name).catch((e) => console.error('Retry failed', e))}
      >
        {t('retryNow')}
      </button>
    </div>
  )
}
