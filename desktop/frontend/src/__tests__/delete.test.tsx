import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { apiMock, resetApiMock, pushState } from './apiMock'
import { appState, server, device } from './fixtures'
import { setLang } from '../i18n'
import App from '../App'

vi.mock('../api', async () => (await import('./apiMock')).apiMock)

const state = () =>
  appState({
    servers: [server({ devices: [device({ name: 'a', on: true }), device({ name: 'b', on: true }), device({ name: 'c', on: false })] })],
    selected: 'home',
  })

async function open() {
  apiMock.GetState.mockResolvedValue(state())
  render(<App />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: '删除 home' }))
  return { user, dlg: screen.getByRole('dialog') }
}

describe('Delete confirm', () => {
  beforeEach(() => {
    resetApiMock()
    setLang('zh')
  })

  it('uses the plan text with n = open forwards, focuses 取消', async () => {
    const { dlg } = await open()
    expect(dlg.textContent).toContain('删除 home？会停止在它上面的共享和 2 个转发，并删除这台服务器在本机的配置。服务器上的数据不受影响。')
    expect(document.activeElement).toBe(screen.getByRole('button', { name: '取消' }))
    expect(screen.getByRole('button', { name: '删除' }).className).toContain('dangerfill')
  })

  it('confirming calls DeleteServer and closes', async () => {
    const { user } = await open()
    await user.click(screen.getByRole('button', { name: '删除' }))
    expect(apiMock.DeleteServer).toHaveBeenCalledWith('home')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('cancel closes without deleting', async () => {
    const { user } = await open()
    await user.click(screen.getByRole('button', { name: '取消' }))
    expect(apiMock.DeleteServer).not.toHaveBeenCalled()
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('cancelled: keeps the dialog open with a note; buttons disabled while deleting', async () => {
    let reject!: (e: Error) => void
    apiMock.DeleteServer.mockImplementation(() => new Promise((_, r) => (reject = r)) as any)
    const { user } = await open()
    await user.click(screen.getByRole('button', { name: '删除' }))
    expect((screen.getByRole('button', { name: '取消' }) as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: '删除' }) as HTMLButtonElement).disabled).toBe(true)
    reject(new Error('cancelled: user declined'))
    expect(await screen.findByText('已取消授权，服务器未删除')).toBeTruthy()
    expect(screen.getByRole('dialog')).toBeTruthy()
    expect((screen.getByRole('button', { name: '删除' }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('other errors show the raw message', async () => {
    apiMock.DeleteServer.mockRejectedValue(new Error('unknown: no such server'))
    const { user } = await open()
    await user.click(screen.getByRole('button', { name: '删除' }))
    expect(await screen.findByText('unknown: no such server')).toBeTruthy()
  })

  it('a state event keeps the same dialog element', async () => {
    const { dlg } = await open()
    pushState({ ...state(), selected: 'home' })
    expect(screen.getByRole('dialog')).toBe(dlg)
  })
})
