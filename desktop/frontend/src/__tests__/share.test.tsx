import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { apiMock, resetApiMock } from './apiMock'
import { server } from './fixtures'
import { setLang } from '../i18n'
import ShareSection from '../components/ShareSection'

vi.mock('../api', async () => (await import('./apiMock')).apiMock)

const up = { running: true, port: 22 }
function share(over: Record<string, any> = {}, platform = 'linux', sshd = up) {
  const sv = server()
  sv.share = { ...sv.share, ...over }
  return render(<ShareSection server={sv} platform={platform} sshd={sshd} />)
}

describe('ShareSection', () => {
  beforeEach(() => {
    resetApiMock()
    setLang('zh')
    apiMock.errorCode.mockImplementation(((e: any) => String(e?.message ?? e).split(':')[0]) as any)
    apiMock.errorDetail.mockImplementation(((e: any) => String(e?.message ?? e).split(':').slice(1).join(':').trim()) as any)
  })

  it('name input has the uniqueness subtitle', () => {
    share()
    expect(screen.getByText('在 home 上需唯一')).toBeTruthy()
  })

  it('saves the name on Enter and on blur, but not when unchanged', async () => {
    const u = userEvent.setup()
    share()
    const input = screen.getByLabelText(/设备名字/)
    await u.clear(input)
    await u.type(input, 'laptop{Enter}')
    expect(apiMock.SetShareName).toHaveBeenCalledWith('home', 'laptop')
    apiMock.SetShareName.mockClear()
    await u.clear(input)
    await u.type(input, 'other')
    await u.tab()
    expect(apiMock.SetShareName).toHaveBeenCalledWith('home', 'other')
  })

  it('name_err is shown under the input and the switch is disabled', () => {
    share({ name_err: 'taken' })
    expect(screen.getByText('「macbook」在 home 上已被另一台设备使用,换一个名字')).toBeTruthy()
    expect((screen.getByRole('switch', { name: '共享这台电脑' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('invalid name_err', () => {
    share({ name_err: 'invalid' })
    expect(screen.getByRole('alert').textContent).toContain('名字只能用字母')
  })

  it('windows has no unattended option', () => {
    share({}, 'windows')
    expect(screen.queryByText('无人登录时也保持共享')).toBeNull()
  })

  it('failed: error, retry and view logs', async () => {
    const u = userEvent.setup()
    share({ on: true, phase: 'failed', err: 'auth failed' })
    expect(screen.getByText('auth failed')).toBeTruthy()
    expect(screen.getByText('连接失败,已停止重试')).toBeTruthy()
    await u.click(screen.getByText('重试'))
    expect(apiMock.RetryShare).toHaveBeenCalledWith('home')
    await u.click(screen.getByText('查看日志'))
    expect(apiMock.OpenLog).toHaveBeenCalledWith('home')
  })

  it('journal log error shows the command as copyable text; nolog shows the missing-file note', async () => {
    const u = userEvent.setup()
    share({ on: true, phase: 'failed', err: 'x' })
    apiMock.OpenLog.mockRejectedValueOnce(new Error('journal: journalctl --user -u tunnel-home -f'))
    await u.click(screen.getByText('查看日志'))
    expect(await screen.findByText('journalctl --user -u tunnel-home -f')).toBeTruthy()
    apiMock.OpenLog.mockRejectedValueOnce(new Error('nolog: /x'))
    await u.click(screen.getByText('查看日志'))
    expect(await screen.findByText('日志文件还不存在')).toBeTruthy()
  })

  it('sshd not running: hint per OS and switch disabled', () => {
    share({}, 'darwin', { running: false, port: 0 })
    expect(screen.getByText(/在 系统设置 → 通用 → 共享 中打开“远程登录”/)).toBeTruthy()
    expect((screen.getByRole('switch', { name: '共享这台电脑' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('outdated: update button calls UpdateShare', async () => {
    share({ on: true, phase: 'ready', outdated: true })
    await userEvent.click(screen.getByText('更新共享服务'))
    expect(apiMock.UpdateShare).toHaveBeenCalledWith('home')
  })

  it('darwin asks the FileVault question before SetShareSystem', async () => {
    const u = userEvent.setup()
    share({}, 'darwin')
    await u.click(screen.getByRole('switch', { name: '无人登录时也保持共享' }))
    expect(apiMock.SetShareSystem).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog').textContent).toContain('开机界面输入密码')
    await u.click(screen.getByText('继续'))
    expect(apiMock.SetShareSystem).toHaveBeenCalledWith('home', true)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('declining the FileVault question does nothing', async () => {
    const u = userEvent.setup()
    share({}, 'darwin')
    await u.click(screen.getByRole('switch', { name: '无人登录时也保持共享' }))
    await u.click(screen.getByText('取消'))
    expect(apiMock.SetShareSystem).not.toHaveBeenCalled()
  })

  it('linux sets the system mode directly; cancelled shows a note', async () => {
    apiMock.SetShareSystem.mockRejectedValueOnce(new Error('cancelled: x'))
    share({}, 'linux')
    await userEvent.click(screen.getByRole('switch', { name: '无人登录时也保持共享' }))
    expect(apiMock.SetShareSystem).toHaveBeenCalledWith('home', true)
    expect(await screen.findByText('已取消授权')).toBeTruthy()
  })

  it('busy disables the controls and shows 正在处理…', () => {
    share({ busy: true })
    expect(screen.getByText('正在处理…')).toBeTruthy()
    expect((screen.getByRole('switch', { name: '共享这台电脑' }) as HTMLButtonElement).disabled).toBe(true)
    const input = screen.getByLabelText(/设备名字/) as HTMLInputElement
    // readOnly (not disabled) so a focused input keeps its focus state
    expect(input.disabled).toBe(false)
    expect(input.readOnly).toBe(true)
    expect(input.getAttribute('aria-busy')).toBe('true')
  })

  it('sshd off sentences are one full sentence per OS', () => {
    const cases: [string, string][] = [
      ['darwin', '本机 SSH 服务未开启,不能共享。在 系统设置 → 通用 → 共享 中打开“远程登录”'],
      ['linux', '本机 SSH 服务未开启,不能共享。安装并启动 openssh-server'],
      ['windows', '本机 SSH 服务未开启,不能共享。在 设置 → 系统 → 可选功能 中添加“OpenSSH 服务器”并启动服务'],
    ]
    for (const [os, text] of cases) {
      const { container, unmount } = share({}, os, { running: false, port: 0 })
      expect(container.querySelector('.sshd-off')!.textContent).toBe(text)
      unmount()
    }
  })

  it('English sshd sentence has a space between problem and fix', () => {
    setLang('en')
    const { container } = share({}, 'linux', { running: false, port: 0 })
    expect(container.querySelector('.sshd-off')!.textContent).toBe('The SSH service is off, so sharing is unavailable. Install and start openssh-server')
  })

  it('a no_sshd rejection shows the same sentence', async () => {
    apiMock.SetShare.mockRejectedValueOnce(new Error('no_sshd: none'))
    share({}, 'linux')
    await userEvent.click(screen.getByRole('switch', { name: '共享这台电脑' }))
    const note = await screen.findByRole('status')
    expect(note.textContent).toBe('本机 SSH 服务未开启,不能共享。安装并启动 openssh-server')
  })

  it('an active share can always be switched off (sshd down or name_err)', async () => {
    share({ on: true, phase: 'ready', name_err: 'taken' }, 'linux', { running: false, port: 0 })
    const sw = screen.getByRole('switch', { name: '共享这台电脑' }) as HTMLButtonElement
    expect(sw.disabled).toBe(false)
    await userEvent.click(sw)
    expect(apiMock.SetShare).toHaveBeenCalledWith('home', false)
  })

  it('name_taken rejection names the attempted name and clears when editing', async () => {
    const u = userEvent.setup()
    apiMock.SetShareName.mockRejectedValueOnce(new Error('name_taken: x'))
    share()
    const input = screen.getByLabelText(/设备名字/)
    await u.clear(input)
    await u.type(input, 'nas{Enter}')
    expect(await screen.findByText('「nas」在 home 上已被另一台设备使用,换一个名字')).toBeTruthy()
    await u.type(input, '2')
    expect(screen.queryByText(/已被另一台设备使用/)).toBeNull()
  })

  it('copy shows 复制失败 without a clipboard API', async () => {
    const u = userEvent.setup()
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    share({ on: true, phase: 'failed', err: 'x' })
    apiMock.OpenLog.mockRejectedValueOnce(new Error('journal: journalctl -u x'))
    await u.click(screen.getByText('查看日志'))
    await u.click(await screen.findByText('复制'))
    expect(await screen.findByText('复制失败')).toBeTruthy()
  })

  it('share switch calls SetShare without flipping itself', async () => {
    share()
    const sw = screen.getByRole('switch', { name: '共享这台电脑' })
    await userEvent.click(sw)
    expect(apiMock.SetShare).toHaveBeenCalledWith('home', true)
    expect(sw.getAttribute('aria-checked')).toBe('false')
  })
})
