import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { apiMock, resetApiMock } from './apiMock'
import { device, server } from './fixtures'
import { setLang } from '../i18n'
import DeviceRow from '../components/DeviceRow'
import ServerDetail from '../components/ServerDetail'

vi.mock('../api', async () => (await import('./apiMock')).apiMock)

const noop = () => {}
function row(d = {}, extra: Record<string, any> = {}) {
  const dev = device({ name: 'nas', os: 'linux', os_version: '24.04', ...d })
  return render(<DeviceRow server={server({ devices: [dev] })} device={dev} onCommands={noop} {...extra} />)
}

describe('DeviceRow', () => {
  beforeEach(() => {
    resetApiMock()
    setLang('zh')
    apiMock.errorCode.mockImplementation(((e: any) => String(e?.message ?? e).split(':')[0]) as any)
    apiMock.errorDetail.mockImplementation(((e: any) => String(e?.message ?? e).split(':').slice(1).join(':').trim()) as any)
  })

  it('orders identity, connection, separator, commands', () => {
    const { container } = row({ started_at: new Date(Date.now() - 3 * 86400_000 - 5000).toISOString() })
    const drow = container.querySelector('.drow')!
    const kids = Array.from(drow.children).map((c) => c.className)
    expect(kids).toEqual(['dot on', 'grow', 'conn', 'vsep', 'copy'])
    expect(drow.querySelector('.name')!.textContent).toBe('nas')
    expect(drow.querySelector('.meta')!.textContent).toContain('Linux 24.04')
    expect(drow.querySelector('.meta')!.textContent).toContain('在线 3 天')
    expect(drow.querySelector('.conn')!.children[0].className).toContain('port')
    expect(drow.querySelector('.conn')!.children[1].getAttribute('role')).toBe('switch')
  })

  it('offline device: switch disabled, last seen shown', () => {
    row({ online: false, last_seen: new Date(Date.now() - 2 * 3600_000 - 5000).toISOString() })
    expect((screen.getByRole('switch') as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByText('离线 · 最后在线 2 小时前')).toBeTruthy()
  })

  it('offline device with the forward on keeps the switch usable and says it resumes', () => {
    row({ online: false, on: true })
    expect(screen.getByText('离线,上线后恢复')).toBeTruthy()
    expect((screen.getByRole('switch') as HTMLButtonElement).disabled).toBe(false)
  })

  it('port number turns into an input; Enter calls SetPort, Esc does not', async () => {
    const u = userEvent.setup()
    row()
    await u.click(screen.getByRole('button', { name: /5102/ }))
    let input = screen.getByRole('textbox') as HTMLInputElement
    expect(input.value).toBe('5102')
    await u.clear(input)
    await u.type(input, '6000{Enter}')
    expect(apiMock.SetPort).toHaveBeenCalledWith('home', 'nas', 6000)

    apiMock.SetPort.mockClear()
    await u.click(screen.getByRole('button', { name: /5102/ }))
    input = screen.getByRole('textbox') as HTMLInputElement
    await u.clear(input)
    await u.type(input, '7000{Escape}')
    expect(apiMock.SetPort).not.toHaveBeenCalled()
    expect(screen.queryByRole('textbox')).toBeNull()
  })

  it('rejects an out-of-range port before calling SetPort', async () => {
    const u = userEvent.setup()
    row()
    await u.click(screen.getByRole('button', { name: /5102/ }))
    const input = screen.getByRole('textbox')
    await u.clear(input)
    await u.type(input, '80{Enter}')
    expect(apiMock.SetPort).not.toHaveBeenCalled()
    expect(screen.getByText('端口需在 1024–65535 之间')).toBeTruthy()
    expect(screen.getByRole('textbox')).toBeTruthy()
  })

  it('shows a SetPort rejection mapped to a sentence and keeps editing', async () => {
    const u = userEvent.setup()
    apiMock.SetPort.mockRejectedValueOnce(new Error('port_busy: in use'))
    row()
    await u.click(screen.getByRole('button', { name: /5102/ }))
    const input = screen.getByRole('textbox')
    await u.clear(input)
    await u.type(input, '6000{Enter}')
    expect(await screen.findByText('端口 6000 已被其他程序占用')).toBeTruthy()
  })

  it('connected port uses .port.live', () => {
    const { container } = row({ on: true })
    expect(container.querySelector('.port.live')).toBeTruthy()
  })

  it('off, offline or not-ready ports are not live', () => {
    expect(row({ on: false }).container.querySelector('.port.live')).toBeNull()
    expect(row({ on: true, online: false }).container.querySelector('.port.live')).toBeNull()
    const dev = device({ name: 'nas', on: true })
    const waiting = render(<DeviceRow server={server({ conn: 'waiting', devices: [dev] })} device={dev} onCommands={noop} />)
    expect(waiting.container.querySelector('.port.live')).toBeNull()
  })

  it('maps port_conflict rejections by code', async () => {
    const u = userEvent.setup()
    apiMock.SetPort.mockRejectedValueOnce(new Error('port_conflict: x'))
    row()
    await u.click(screen.getByRole('button', { name: /5102/ }))
    const input = screen.getByRole('textbox')
    await u.clear(input)
    await u.type(input, '6001{Enter}')
    expect(await screen.findByText('端口 6001 已分配给其他设备')).toBeTruthy()
  })

  it('relative times refresh on the shared clock', () => {
    vi.useFakeTimers({ now: Date.parse('2026-11-23T10:00:00Z') })
    try {
      row({ started_at: '2026-11-23T09:50:00Z' })
      expect(screen.getByText(/在线 10 分钟/)).toBeTruthy()
      act(() => {
        vi.advanceTimersByTime(5 * 60_000)
      })
      expect(screen.getByText(/在线 15 分钟/)).toBeTruthy()
    } finally {
      vi.useRealTimers()
    }
  })

  it('switch calls SetForward and does not flip itself', async () => {
    row()
    const sw = screen.getByRole('switch')
    await userEvent.click(sw)
    expect(apiMock.SetForward).toHaveBeenCalledWith('home', 'nas', true)
    expect(sw.getAttribute('aria-checked')).toBe('false')
  })

  it('maps a SetForward offline rejection under the row', async () => {
    apiMock.SetForward.mockRejectedValueOnce(new Error('offline: gone'))
    row()
    await userEvent.click(screen.getByRole('switch'))
    expect(await screen.findByText('nas 离线,不能连接')).toBeTruthy()
  })

  it('shows state device.err sentences', () => {
    row({ err: 'port_busy' })
    expect(screen.getByText('端口 5102 已被其他程序占用')).toBeTruthy()
  })

  it('disabled turns every control off', () => {
    row({}, { disabled: true })
    expect((screen.getByRole('switch') as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: /5102/ }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('English wording', () => {
    setLang('en')
    row({ online: false })
    expect((screen.getByRole('switch') as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByText('Offline')).toBeTruthy()
  })
})

describe('ServerDetail devices and commands dialog', () => {
  beforeEach(() => {
    resetApiMock()
    setLang('zh')
  })

  it('opens the commands dialog with one 完成 button and copies a command', async () => {
    const u = userEvent.setup()
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const sv = server({ devices: [device({ name: 'nas', port: 5103, on: true })] })
    render(<ServerDetail server={sv} platform="linux" sshd={{ running: true, port: 22 }} />)
    await u.click(screen.getByRole('button', { name: /连接命令: nas/ }))
    expect(screen.getByText('nas 的连接命令')).toBeTruthy()
    expect(screen.getByText('转发已打开,连接 127.0.0.1:5103 就是 nas')).toBeTruthy()
    const dlg = screen.getByRole('dialog')
    const buttons = Array.from(dlg.querySelectorAll('.dlg-f button'))
    expect(buttons.map((b) => b.textContent)).toEqual(['完成'])
    await u.click(screen.getByRole('button', { name: '复制 ssh' }))
    expect(writeText).toHaveBeenCalledWith('ssh -p 5103 <USERNAME>@127.0.0.1')
    expect(await screen.findByText('已复制')).toBeTruthy()
    await u.click(screen.getByText('完成'))
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('disconnected server: content faded, aria-disabled, controls disabled', () => {
    const sv = server({ conn: 'disconnected', devices: [device({ name: 'nas' })] })
    const { container } = render(<ServerDetail server={sv} platform="linux" sshd={{ running: true, port: 22 }} />)
    expect(container.querySelector('.body.faded')!.getAttribute('aria-disabled')).toBe('true')
    expect(screen.getByRole('switch', { name: '转发到 nas' }).hasAttribute('disabled')).toBe(true)
    expect(screen.getByRole('switch', { name: '共享这台电脑' }).hasAttribute('disabled')).toBe(true)
  })

  it('a state update keeps the port editor and its focus (no remount)', async () => {
    const u = userEvent.setup()
    const sv = (n: number) => server({ devices: [device({ name: 'nas', port: 5102, last_seen: String(n) })] })
    const view = render(<ServerDetail server={sv(1)} platform="linux" sshd={{ running: true, port: 22 }} />)
    await u.click(screen.getByRole('button', { name: /5102/ }))
    const input = screen.getByLabelText('nas 的本机端口')
    await u.type(input, '1')
    view.rerender(<ServerDetail server={sv(2)} platform="linux" sshd={{ running: true, port: 22 }} />)
    expect(screen.getByLabelText('nas 的本机端口')).toBe(input)
    expect(document.activeElement).toBe(input)
    expect((input as HTMLInputElement).value).toBe('51021')
  })

  it('keeps the same DOM node per device name when devices are reordered or inserted', () => {
    const props = { platform: 'linux', sshd: { running: true, port: 22 } }
    const mk = (names: string[]) => server({ devices: names.map((n) => device({ name: n })) })
    const view = render(<ServerDetail server={mk(['a', 'b'])} {...props} />)
    const nodeOf = (n: string) => screen.getByText(n, { selector: '.name' }).closest('.item')
    const b = nodeOf('b')
    view.rerender(<ServerDetail server={mk(['c', 'a', 'b'])} {...props} />)
    expect(nodeOf('b')).toBe(b)
  })

  it('closes the commands dialog when its device disappears and does not reopen it', async () => {
    const u = userEvent.setup()
    const props = { platform: 'linux', sshd: { running: true, port: 22 } }
    const withDev = server({ devices: [device({ name: 'nas' })] })
    const view = render(<ServerDetail server={withDev} {...props} />)
    await u.click(screen.getByRole('button', { name: /连接命令: nas/ }))
    expect(screen.getByRole('dialog')).toBeTruthy()
    view.rerender(<ServerDetail server={server({ devices: [] })} {...props} />)
    expect(screen.queryByRole('dialog')).toBeNull()
    view.rerender(<ServerDetail server={withDev} {...props} />)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('copy shows 复制失败 when the clipboard API is missing', async () => {
    const u = userEvent.setup()
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    render(<ServerDetail server={server({ devices: [device({ name: 'nas' })] })} platform="linux" sshd={{ running: true, port: 22 }} />)
    await u.click(screen.getByRole('button', { name: /连接命令: nas/ }))
    await u.click(screen.getByRole('button', { name: '复制 ssh' }))
    expect(await screen.findByText('复制失败')).toBeTruthy()
    expect(screen.queryByText('已复制')).toBeNull()
  })
})
