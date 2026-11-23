import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { apiMock, resetApiMock, stateListenerCount } from './apiMock'
import { appState, server } from './fixtures'
import { setLang } from '../i18n'
import App from '../App'

vi.mock('../api', async () => (await import('./apiMock')).apiMock)

async function renderOn(platform: string) {
  apiMock.GetState.mockResolvedValue(appState({ platform, servers: [server()], selected: 'home' }))
  const view = render(<App />)
  await screen.findByText('已连接 1 在线 3')
  return view
}

describe('platform layout', () => {
  beforeEach(() => {
    resetApiMock()
    setLang('zh')
  })

  it('darwin reserves a drag region for the traffic lights', async () => {
    const { container } = await renderOn('darwin')
    expect(container.querySelector('.app')!.getAttribute('data-platform')).toBe('darwin')
    expect(container.querySelector('.sidebar .side-top')).toBeTruthy()
  })

  for (const p of ['windows', 'linux']) {
    it(`${p} has no inset and no self-drawn window buttons`, async () => {
      const { container } = await renderOn(p)
      expect(container.querySelector('.app')!.getAttribute('data-platform')).toBe(p)
      expect(container.querySelector('.side-top')).toBeNull()
      expect(container.querySelector('.lights')).toBeNull()
      for (const n of [/最小化/, /最大化/, /关闭窗口/, /minimi[sz]e/i, /maximi[sz]e/i]) {
        expect(screen.queryByRole('button', { name: n })).toBeNull()
      }
    })
  }

  it('shows the empty state with no servers', async () => {
    apiMock.GetState.mockResolvedValue(appState())
    render(<App />)
    expect(await screen.findByText('添加第一台服务器')).toBeTruthy()
    expect(screen.getAllByRole('button', { name: '添加服务器' }).length).toBe(2)
  })

  it('puts the theme on the document element too', async () => {
    apiMock.GetState.mockResolvedValue(
      appState({ servers: [server()], selected: 'home', settings: { ...appState().settings, mode: 'dark', accent: 'violet' } }),
    )
    const { unmount } = render(<App />)
    await screen.findByText('已连接 1 在线 3')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(document.documentElement.dataset.accent).toBe('violet')
    unmount()
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(document.documentElement.dataset.accent).toBeUndefined()
  })

  it('unmounting removes the state listener', async () => {
    apiMock.GetState.mockResolvedValue(appState())
    const { unmount } = render(<App />)
    await screen.findByText('添加第一台服务器')
    expect(stateListenerCount()).toBe(1)
    unmount()
    expect(stateListenerCount()).toBe(0)
  })

  it('mode "system" follows prefers-color-scheme changes', async () => {
    let matches = false
    const listeners = new Set<() => void>()
    const original = window.matchMedia
    window.matchMedia = ((q: string) => ({
      media: q,
      get matches() {
        return matches
      },
      addEventListener: (_: string, cb: () => void) => listeners.add(cb),
      removeEventListener: (_: string, cb: () => void) => listeners.delete(cb),
    })) as any
    try {
      apiMock.GetState.mockResolvedValue(appState({ servers: [server()], selected: 'home' }))
      const { container } = render(<App />)
      await screen.findByText('已连接 1 在线 3')
      const root = container.querySelector('.app')!
      expect(root.classList.contains('dark')).toBe(false)
      matches = true
      act(() => listeners.forEach((l) => l()))
      expect(root.classList.contains('dark')).toBe(true)
      matches = false
      act(() => listeners.forEach((l) => l()))
      expect(root.classList.contains('dark')).toBe(false)
    } finally {
      window.matchMedia = original
    }
  })
})
