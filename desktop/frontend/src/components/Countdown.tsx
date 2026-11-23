import { useEffect, useState } from 'react'
import { t } from '../i18n'

function remaining(at: string): number {
  const ms = Date.parse(at) - Date.now()
  return Number.isNaN(ms) ? 0 : Math.max(0, Math.ceil(ms / 1000))
}

/** "N 秒后重试"; ticks on its own so the surrounding row is never re-rendered. */
export default function Countdown({ at }: { at: string }) {
  const [s, setS] = useState(() => remaining(at))
  useEffect(() => {
    setS(remaining(at))
    const id = setInterval(() => setS(remaining(at)), 1000)
    return () => clearInterval(id)
  }, [at])
  return <span className="cd">{t('retryIn', { s })}</span>
}
