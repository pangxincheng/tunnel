import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'
import { apiMock, resetApiMock } from './apiMock'
import { server } from './fixtures'
import { setLang } from '../i18n'
import Banner from '../components/Banner'

vi.mock('../api', async () => (await import('./apiMock')).apiMock)

const NOW = Date.parse('2026-11-23T10:00:00Z')
const iso = (offsetMs: number) => new Date(NOW + offsetMs).toISOString()

describe('Banner', () => {
  beforeEach(() => {
    resetApiMock()
    setLang('zh')
    vi.useFakeTimers({ now: NOW })
  })
  afterEach(() => vi.useRealTimers())

  it('waiting: the spec sentence, countdown and retry now', () => {
    const { container } = render(
      <Banner
        server={server({
          conn: 'waiting',
          conn_err: 'connection timed out',
          devices_at: iso(-5 * 60_000 - 10_000),
          retry_at: iso(7_500),
        })}
      />,
    )
    expect(container.textContent).toBe('无法连接 home:connection timed out。设备列表是 5 分钟前的。 8 秒后重试立即重试')
    fireEvent.click(screen.getByText('立即重试'))
    expect(apiMock.Retry).toHaveBeenCalledWith('home')
  })

  it('English sentence punctuation', () => {
    setLang('en')
    const { container } = render(
      <Banner server={server({ conn: 'waiting', conn_err: 'timed out', devices_at: iso(-5 * 60_000), retry_at: iso(3000) })} />,
    )
    expect(container.textContent).toBe('Can’t reach home: timed out.The device list is 5 min old. Retrying in 3sRetry now')
  })

  it('countdown ticks by itself', () => {
    render(<Banner server={server({ conn: 'waiting', retry_at: iso(3_000) })} />)
    expect(screen.getByText('3 秒后重试')).toBeTruthy()
    act(() => {
      vi.advanceTimersByTime(2000)
    })
    expect(screen.getByText('1 秒后重试')).toBeTruthy()
  })

  it('the list age refreshes on the shared clock without a state change', () => {
    render(<Banner server={server({ conn: 'failed', conn_err: 'x', devices_at: iso(-60_000) })} />)
    expect(screen.getByText(/设备列表是 1 分钟前的/)).toBeTruthy()
    act(() => {
      vi.advanceTimersByTime(90_000)
    })
    expect(screen.getByText(/设备列表是 2 分钟前的/)).toBeTruthy()
  })

  it('failed: no countdown', () => {
    render(<Banner server={server({ conn: 'failed', conn_err: 'auth' })} />)
    expect(screen.getByText('立即重试')).toBeTruthy()
    expect(screen.queryByText(/秒后重试/)).toBeNull()
  })

  it('disconnected: grey banner with 连接', () => {
    const { container } = render(<Banner server={server({ conn: 'disconnected' })} />)
    expect(container.querySelector('.banner.paused')).toBeTruthy()
    expect(screen.getByText('已断开 home:共享和转发都已停止,开关状态会保留')).toBeTruthy()
    fireEvent.click(screen.getByText('连接'))
    expect(apiMock.SetConnected).toHaveBeenCalledWith('home', true)
  })

  it('a failed connect shows the mapped sentence in the banner', async () => {
    apiMock.SetConnected.mockRejectedValueOnce(new Error('cancelled: authorization cancelled'))
    render(<Banner server={server({ conn: 'disconnected' })} />)
    await act(async () => {
      fireEvent.click(screen.getByText('连接'))
    })
    expect(screen.getByRole('alert').textContent).toBe('已取消授权')
    apiMock.SetConnected.mockRejectedValueOnce(new Error('tunnel resume: boom'))
    await act(async () => {
      fireEvent.click(screen.getByText('连接'))
    })
    expect(screen.getByRole('alert').textContent).toBe('tunnel resume: boom')
  })

  it('shows nothing when ready or connecting', () => {
    expect(render(<Banner server={server({ conn: 'ready' })} />).container.firstChild).toBeNull()
    expect(render(<Banner server={server({ conn: 'connecting' })} />).container.firstChild).toBeNull()
  })
})
