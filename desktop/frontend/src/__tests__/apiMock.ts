import { vi } from 'vitest'
import { act } from '@testing-library/react'

const CODES = ['offline', 'no_sshd', 'name_taken', 'cancelled', 'not_running', 'port_busy', 'port_invalid', 'port_conflict', 'unknown', 'passphrase', 'auth', 'dial', 'socketdir']
const msgOf = (e: unknown) => (e instanceof Error ? e.message : String(e))
const codeOf = (m: string) => {
  const c = m.slice(0, Math.max(m.indexOf(':'), 0)).trim()
  return CODES.includes(c) ? c : ''
}

const handlers = new Set<(s: any) => void>()

export const apiMock = {
  GetState: vi.fn(),
  Select: vi.fn(async (_n: string) => {}),
  SetConnected: vi.fn(async (_n: string, _on: boolean) => {}),
  Retry: vi.fn(async (_n: string) => {}),
  SetForward: vi.fn(async (_s: string, _d: string, _on: boolean) => {}),
  SetPort: vi.fn(async (_s: string, _d: string, _p: number) => {}),
  SetShare: vi.fn(async (_s: string, _on: boolean) => {}),
  SetShareName: vi.fn(async (_s: string, _n: string) => {}),
  SetShareSystem: vi.fn(async (_s: string, _on: boolean) => {}),
  RetryShare: vi.fn(async (_s: string) => {}),
  UpdateShare: vi.fn(async (_s: string) => {}),
  OpenLog: vi.fn(async (_s: string) => {}),
  UpdateSettings: vi.fn(async (_s: any) => {}),
  AppVersion: vi.fn(async () => '0.1.0'),
  onState: vi.fn((cb: (s: any) => void) => {
    handlers.add(cb)
    return () => {
      handlers.delete(cb)
    }
  }),
  AddServer: vi.fn(async (_s: any) => {}),
  UpdateServer: vi.fn(async (_o: string, _s: any) => {}),
  DeleteServer: vi.fn(async (_n: string) => {}),
  TestConnection: vi.fn(async (_p: any) => ({ fingerprint: '', host_key: '', socket_dir: '' })),
  PickKeyFile: vi.fn(async () => ''),
  errorCode: vi.fn((e: unknown) => codeOf(msgOf(e))),
  errorDetail: vi.fn((e: unknown) => {
    const m = msgOf(e)
    return codeOf(m) ? m.slice(m.indexOf(':') + 1).trim() : m
  }),
  errorMessage: vi.fn((e: unknown) => msgOf(e)),
}

export function stateListenerCount() {
  return handlers.size
}

export function pushState(s: unknown) {
  act(() => {
    handlers.forEach((h) => h(s))
  })
}

export function resetApiMock() {
  handlers.clear()
  for (const v of Object.values(apiMock)) (v as any).mockClear?.()
  apiMock.AppVersion.mockImplementation(async () => '0.1.0')
  apiMock.AddServer.mockImplementation(async () => {})
  apiMock.UpdateServer.mockImplementation(async () => {})
  apiMock.DeleteServer.mockImplementation(async () => {})
  apiMock.TestConnection.mockImplementation(async () => ({ fingerprint: '', host_key: '', socket_dir: '' }))
  apiMock.PickKeyFile.mockImplementation(async () => '')
}
