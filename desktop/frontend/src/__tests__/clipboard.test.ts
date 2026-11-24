import { describe, it, expect, vi, afterEach } from 'vitest'
import { copyText } from '../clipboard'

afterEach(() => Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true }))

describe('copyText', () => {
  it('rejects when the clipboard API is missing', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    await expect(copyText('x')).rejects.toThrow()
  })
  it('writes through navigator.clipboard', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    await copyText('abc')
    expect(writeText).toHaveBeenCalledWith('abc')
  })
})
