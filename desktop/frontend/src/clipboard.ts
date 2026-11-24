import { useCallback, useEffect, useRef, useState } from 'react'

/** Writes text to the clipboard; rejects when the API is missing or refuses. */
export function copyText(text: string): Promise<void> {
  const cb = typeof navigator === 'undefined' ? undefined : navigator.clipboard
  if (!cb || typeof cb.writeText !== 'function') return Promise.reject(new Error('clipboard unavailable'))
  return cb.writeText(text)
}

export type CopyResult = { id: string; ok: boolean } | null

/** Copy with a short "copied / failed" flash per id; the timer is cleared on unmount. */
export function useCopy(flashMs = 1500): [CopyResult, (id: string, text: string) => void] {
  const [result, setResult] = useState<CopyResult>(null)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
      clearTimeout(timer.current)
    }
  }, [])
  const copy = useCallback(
    (id: string, text: string) => {
      const show = (ok: boolean) => {
        if (!alive.current) return
        setResult({ id, ok })
        clearTimeout(timer.current)
        timer.current = setTimeout(() => setResult(null), flashMs)
      }
      copyText(text).then(
        () => show(true),
        () => show(false),
      )
    },
    [flashMs],
  )
  return [result, copy]
}
