// The only module that imports the generated bindings and the Wails runtime.
// Everything else imports from here (tests mock this module).
import { Events } from '@wailsio/runtime'
import * as AppService from '../bindings/github.com/pangxincheng/tunnel/desktop/appservice'

export type { AppState, ServerView, DeviceView, ShareView } from '../bindings/github.com/pangxincheng/tunnel/desktop/internal/core/models'
export type { Settings, Server } from '../bindings/github.com/pangxincheng/tunnel/desktop/internal/config/models'

import type { AppState } from '../bindings/github.com/pangxincheng/tunnel/desktop/internal/core/models'

export const {
  GetState,
  Select,
  Refresh,
  Retry,
  SetConnected,
  SetForward,
  SetPort,
  SetShare,
  SetShareName,
  SetShareSystem,
  RetryShare,
  UpdateShare,
  OpenLog,
  StartLog,
  StopLog,
  RemoveDevice,
  AddServer,
  UpdateServer,
  DeleteServer,
  TestConnection,
  PickKeyFile,
  UpdateSettings,
  AppVersion,
  Quit,
} = AppService

/** Subscribes to the backend's `state` event; returns the unsubscribe function. */
export function onState(cb: (s: AppState) => void): () => void {
  return Events.On('state', (ev) => cb(ev.data as AppState))
}

/** One piece of a share log stream (see StartLog); the last has done set. */
export interface LogChunk {
  id: number
  data: string
  done: boolean
  err: string
}

/** Subscribes to share log chunks; returns the unsubscribe function. */
export function onLog(cb: (c: LogChunk) => void): () => void {
  return Events.On('share-log', (ev) => cb(ev.data as LogChunk))
}

const CODES = [
  'offline', 'no_sshd', 'name_taken', 'cancelled', 'not_running', 'port_busy', 'port_invalid', 'port_conflict', 'forward_on', 'online', 'not_connected', 'unknown',
  'passphrase', 'auth', 'dial', 'socketdir', 'autostart', 'journal', 'nolog',
]

function message(e: unknown): string {
  if (e instanceof Error) return e.message
  if (typeof e === 'string') return e
  return String((e as { message?: unknown })?.message ?? e)
}

/** The full error message, code prefix included. */
export const errorMessage = message

/** Code prefix of a backend error ("" when the message has no known code). */
export function errorCode(e: unknown): string {
  const m = message(e)
  const i = m.indexOf(':')
  if (i < 0) return ''
  const c = m.slice(0, i).trim()
  return CODES.includes(c) ? c : ''
}

/** The error text after the code prefix (the whole message when there is none). */
export function errorDetail(e: unknown): string {
  const m = message(e)
  return errorCode(e) ? m.slice(m.indexOf(':') + 1).trim() : m
}
