import { describe, it, expect, vi } from 'vitest'

vi.mock('@wailsio/runtime', () => ({ Events: { On: vi.fn() } }))

import { errorCode, errorDetail } from '../api'

describe('api error codes', () => {
  it.each(['offline', 'port_busy', 'port_invalid', 'port_conflict', 'name_taken', 'cancelled', 'not_running', 'journal', 'nolog'])(
    'recognises %s',
    (c) => {
      expect(errorCode(new Error(`${c}: detail here`))).toBe(c)
      expect(errorDetail(new Error(`${c}: detail here`))).toBe('detail here')
    },
  )
  it('unknown prefixes are not codes', () => {
    expect(errorCode(new Error('boom: x'))).toBe('')
    expect(errorDetail(new Error('boom: x'))).toBe('boom: x')
  })
})
