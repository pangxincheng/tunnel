import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { apiMock, resetApiMock } from './apiMock'
import { appState, server } from './fixtures'
import { setLang } from '../i18n'
import Sidebar from '../components/Sidebar'

vi.mock('../api', async () => (await import('./apiMock')).apiMock)

const noop = () => {}
function renderSidebar(servers: any[], handlers: Record<string, any> = {}) {
  return render(
    <Sidebar
      state={appState({ servers, selected: servers[0]?.name ?? '' })}
      onAdd={noop}
      onEdit={noop}
      onDelete={noop}
      onSettings={noop}
      {...handlers}
    />,
  )
}

describe('Sidebar', () => {
  beforeEach(() => {
    resetApiMock()
    setLang('zh')
  })

  it('shows the connected/online summary', () => {
    const { container } = renderSidebar([server({ connected: 1, online: 3 })])
    // the raw text separates the two counts with U+3000; testing-library collapses it to a space
    expect(container.querySelector('.srv .m')!.textContent).toBe('已连接 1\u3000在线 3')
    expect(screen.getByText('已连接 1 在线 3')).toBeTruthy()
  })

  it('waiting connection: red dot and 无法连接', () => {
    const { container } = renderSidebar([server({ conn: 'waiting' })])
    expect(screen.getByText('无法连接')).toBeTruthy()
    expect(container.querySelector('.dot.err')).toBeTruthy()
  })

  it('share failure: red dot but no 无法连接', () => {
    const s = server()
    s.share = { ...s.share, on: true, phase: 'failed' }
    const { container } = renderSidebar([s])
    expect(container.querySelector('.dot.err')).toBeTruthy()
    expect(screen.queryByText('无法连接')).toBeNull()
    expect(screen.getByText('已连接 1 在线 3')).toBeTruthy()
  })

  it('disconnected shows 已断开 with an idle dot', () => {
    const { container } = renderSidebar([server({ conn: 'disconnected' })])
    expect(screen.getByText('已断开')).toBeTruthy()
    expect(container.querySelector('.dot.idle')).toBeTruthy()
    expect(container.querySelector('.dot.err')).toBeNull()
  })

  it('row action buttons have aria-labels and call handlers', async () => {
    const onEdit = vi.fn()
    const onDelete = vi.fn()
    renderSidebar([server(), server({ name: 'away', conn: 'disconnected' })], { onEdit, onDelete })
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: '编辑 home' }))
    expect(onEdit).toHaveBeenCalledWith('home')
    await user.click(screen.getByRole('button', { name: '删除 home' }))
    expect(onDelete).toHaveBeenCalledWith('home')
    await user.click(screen.getByRole('button', { name: '断开 home' }))
    expect(apiMock.SetConnected).toHaveBeenCalledWith('home', false)
    await user.click(screen.getByRole('button', { name: '连接 away' }))
    expect(apiMock.SetConnected).toHaveBeenCalledWith('away', true)
  })

  it('clicking a row selects it', async () => {
    renderSidebar([server(), server({ name: 'hk' })])
    await userEvent.setup().click(screen.getByText('hk'))
    expect(apiMock.Select).toHaveBeenCalledWith('hk')
  })

  it('bottom buttons', async () => {
    const onAdd = vi.fn()
    const onSettings = vi.fn()
    renderSidebar([], { onAdd, onSettings })
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: '添加服务器' }))
    await user.click(screen.getByRole('button', { name: '设置' }))
    expect(onAdd).toHaveBeenCalledTimes(1)
    expect(onSettings).toHaveBeenCalledTimes(1)
  })
})
