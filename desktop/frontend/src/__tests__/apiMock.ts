import { vi } from 'vitest'
import { act } from '@testing-library/react'

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
  errorCode: vi.fn(() => ''),
  errorDetail: vi.fn((e: unknown) => String(e)),
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
}
