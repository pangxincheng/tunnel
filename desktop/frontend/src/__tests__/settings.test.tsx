import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { apiMock, resetApiMock, pushState } from './apiMock'
import { appState, server } from './fixtures'
import { setLang } from '../i18n'
import App from '../App'
import { readFileSync } from 'node:fs'

vi.mock('../api', async () => (await import('./apiMock')).apiMock)

const base = () => appState({ servers: [server()], selected: 'home' })

async function openSettings() {
  apiMock.GetState.mockResolvedValue(base())
  const view = render(<App />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: '设置' }))
  return { view, user, dialog: screen.getByRole('dialog') }
}

describe('Settings dialog', () => {
  beforeEach(() => {
    resetApiMock()
    setLang('zh')
  })

  it('choosing dark calls UpdateSettings with mode dark; five accents exist', async () => {
    const { user, dialog } = await openSettings()
    expect(dialog.querySelectorAll('button.swatch').length).toBe(5)
    await user.click(screen.getByRole('button', { name: '深色' }))
    expect(apiMock.UpdateSettings).toHaveBeenCalledTimes(1)
    expect(apiMock.UpdateSettings.mock.calls[0][0]).toMatchObject({ mode: 'dark', accent: 'green', lang: 'zh' })
  })

  it('two quick changes before a state event are both sent', async () => {
    const { user } = await openSettings()
    await user.click(screen.getByRole('button', { name: '深色' }))
    await user.click(screen.getByRole('button', { name: '蓝色' }))
    expect(apiMock.UpdateSettings).toHaveBeenCalledTimes(2)
    expect(apiMock.UpdateSettings.mock.calls[1][0]).toMatchObject({ mode: 'dark', accent: 'blue' })
  })

  it('a newer state replaces the pending merge base', async () => {
    const { user } = await openSettings()
    await user.click(screen.getByRole('button', { name: '深色' }))
    pushState({ ...base(), settings: { ...base().settings, mode: 'light' } })
    await user.click(screen.getByRole('button', { name: '蓝色' }))
    expect(apiMock.UpdateSettings.mock.calls[1][0]).toMatchObject({ mode: 'light', accent: 'blue' })
  })

  it('swatch colours come from CSS, not inline styles', async () => {
    const { dialog } = await openSettings()
    for (const sw of dialog.querySelectorAll<HTMLElement>('button.swatch')) {
      expect(sw.getAttribute('style')).toBeNull()
      expect(sw.dataset.accent).toBeTruthy()
    }
    const css = readFileSync('src/styles/tokens.css', 'utf8')
    for (const a of ['green', 'blue', 'violet', 'amber', 'graphite']) {
      expect(css).toContain(`--accent-${a}:`)
      expect(css.match(new RegExp(`--accent-${a}:`, 'g'))!.length).toBe(2) // light + dark
    }
  })

  it('toggles general switches and shows the version', async () => {
    const { user } = await openSettings()
    expect(await screen.findByText('v0.1.0')).toBeTruthy()
    const restore = screen.getByRole('switch', { name: '启动时自动打开上次的转发' })
    expect(restore.getAttribute('aria-checked')).toBe('true')
    await user.click(restore)
    expect(apiMock.UpdateSettings.mock.calls[0][0]).toMatchObject({ restore_forwards: false })
    await user.click(screen.getByRole('switch', { name: '登录时启动 Tunnel' }))
    expect(apiMock.UpdateSettings.mock.calls[1][0]).toMatchObject({ launch_at_login: true })
  })

  it('TestSettingsUpdatesInPlace', async () => {
    const { user, dialog } = await openSettings()
    const cls = dialog.className
    const settings = base().settings

    await user.click(screen.getByRole('button', { name: '深色' }))
    pushState({ ...base(), settings: { ...settings, mode: 'dark' } })
    expect(screen.getByRole('dialog')).toBe(dialog)
    expect(screen.getByRole('button', { name: '深色' })).toBe(document.activeElement)

    const swatch = dialog.querySelectorAll<HTMLButtonElement>('button.swatch')[1]
    await user.click(swatch)
    pushState({ ...base(), settings: { ...settings, mode: 'dark', accent: 'blue' } })
    expect(screen.getByRole('dialog')).toBe(dialog)
    expect(document.activeElement).toBe(swatch)

    const english = screen.getByRole('button', { name: 'English' })
    await user.click(english)
    pushState({ ...base(), settings: { ...settings, mode: 'dark', accent: 'blue', lang: 'en' } })
    expect(screen.getByRole('dialog')).toBe(dialog)
    expect(document.activeElement).toBe(english)
    expect(screen.getByText('Settings')).toBeTruthy()

    expect(dialog.className).toBe(cls)
    const root = document.querySelector('.app')!
    expect(root.classList.contains('dark')).toBe(true)
    expect(root.getAttribute('data-accent')).toBe('blue')
  })

  it('Esc closes the dialog and returns focus to the gear', async () => {
    const { user } = await openSettings()
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(document.activeElement).toBe(screen.getByRole('button', { name: '设置' }))
  })

  it('shows an autostart failure from UpdateSettings', async () => {
    apiMock.errorCode.mockReturnValue('autostart')
    apiMock.errorDetail.mockReturnValue('boom')
    apiMock.UpdateSettings.mockRejectedValueOnce(new Error('autostart: boom'))
    const { user } = await openSettings()
    await user.click(screen.getByRole('switch', { name: '登录时启动 Tunnel' }))
    await act(async () => {})
    expect(screen.getByRole('alert').textContent).toContain('boom')
  })
})
