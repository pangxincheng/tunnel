import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { apiMock, resetApiMock } from './apiMock'
import { server } from './fixtures'
import { setLang } from '../i18n'
import Banner from '../components/Banner'

vi.mock('../api', async () => (await import('./apiMock')).apiMock)

describe('Banner', () => {
  beforeEach(() => {
    resetApiMock()
    setLang('zh')
  })
  afterEach(() => vi.useRealTimers())

  it('waiting: reason, list age, countdown and retry now', async () => {
    const now = Date.now()
    const { container } = render(
      <Banner
        server={server({
          conn: 'waiting',
          conn_err: 'connection timed out',
          devices_at: new Date(now - 5 * 60_000 - 10_000).toISOString(),
          retry_at: new Date(now + 7_500).toISOString(),
        })}
      />,
    )
    expect(container.textContent).toBe('无法连接 home:connection timed out设备列表是 5 分钟前的。 8 秒后重试立即重试')
    await userEvent.click(screen.getByText('立即重试'))
    expect(apiMock.Retry).toHaveBeenCalledWith('home')
  })

  it('countdown ticks by itself', () => {
    vi.useFakeTimers()
    const at = new Date(Date.now() + 3_000).toISOString()
    render(<Banner server={server({ conn: 'waiting', retry_at: at })} />)
    expect(screen.getByText('3 秒后重试')).toBeTruthy()
    act(() => {
      vi.advanceTimersByTime(2000)
    })
    expect(screen.getByText('1 秒后重试')).toBeTruthy()
  })

  it('failed: no countdown', () => {
    render(<Banner server={server({ conn: 'failed', conn_err: 'auth' })} />)
    expect(screen.getByText('立即重试')).toBeTruthy()
    expect(screen.queryByText(/秒后重试/)).toBeNull()
  })

  it('disconnected: grey banner with 连接', async () => {
    const { container } = render(<Banner server={server({ conn: 'disconnected' })} />)
    expect(container.querySelector('.banner.paused')).toBeTruthy()
    expect(screen.getByText('已断开 home:共享和转发都已停止,开关状态会保留')).toBeTruthy()
    await userEvent.click(screen.getByText('连接'))
    expect(apiMock.SetConnected).toHaveBeenCalledWith('home', true)
  })

  it('shows nothing when ready or connecting', () => {
    expect(render(<Banner server={server({ conn: 'ready' })} />).container.firstChild).toBeNull()
    expect(render(<Banner server={server({ conn: 'connecting' })} />).container.firstChild).toBeNull()
  })
})
