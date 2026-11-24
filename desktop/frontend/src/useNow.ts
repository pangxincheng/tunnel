import { useEffect, useState } from 'react'

// One shared low-frequency clock for relative-time labels ("3 天", "5 分钟前").
const subs = new Set<() => void>()
let timer: ReturnType<typeof setInterval> | undefined
const PERIOD = 30_000

function subscribe(cb: () => void): () => void {
  subs.add(cb)
  if (!timer) timer = setInterval(() => subs.forEach((f) => f()), PERIOD)
  return () => {
    subs.delete(cb)
    if (subs.size === 0 && timer) {
      clearInterval(timer)
      timer = undefined
    }
  }
}

/** Current time in ms, refreshed every 30 s; re-renders only the calling component. */
export function useNow(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    setNow(Date.now())
    return subscribe(() => setNow(Date.now()))
  }, [])
  return now
}
