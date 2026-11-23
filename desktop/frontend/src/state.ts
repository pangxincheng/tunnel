import { useEffect, useState } from 'react'
import { GetState, onState, type AppState } from './api'

/**
 * Live application state: the first value comes from GetState(), later ones
 * from the backend's `state` events. Returns null until the first snapshot.
 */
export function useAppState(): AppState | null {
  const [state, setState] = useState<AppState | null>(null)
  useEffect(() => {
    let live = true
    let gotEvent = false
    const off = onState((s) => {
      gotEvent = true
      if (live) setState(s)
    })
    GetState()
      .then((s) => {
        if (live && !gotEvent) setState(s)
      })
      .catch((e) => console.error('GetState failed', e))
    return () => {
      live = false
      off()
    }
  }, [])
  return state
}
