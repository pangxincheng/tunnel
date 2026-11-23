import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { apiMock, resetApiMock, pushState } from './apiMock'
import { appState, server } from './fixtures'
import { setLang } from '../i18n'
import App from '../App'

vi.mock('../api', async () => (await import('./apiMock')).apiMock)

const FP = 'SHA256:k3Vt9Qe1x0Zb7HnUqP2sLw8RfYc4mD6aJ5oE1iT0gXs'
const probeOk = { fingerprint: FP, host_key: 'ssh-ed25519 AAAAnew', socket_dir: '/run/user/1000/tunnel' }
const base = () => appState({ servers: [server()], selected: 'home' })

async function open(state = base(), via: 'add' | 'edit' = 'add') {
  apiMock.GetState.mockResolvedValue(state)
  render(<App />)
  const user = userEvent.setup()
  if (via === 'add') await user.click(await screen.findByRole('button', { name: '添加服务器' }))
  else await user.click(await screen.findByRole('button', { name: `编辑 ${state.servers![0].name}` }))
  return { user, dlg: screen.getByRole('dialog') }
}

async function fillAdd(user: ReturnType<typeof userEvent.setup>, over: Record<string, string> = {}) {
  const v = { 名称: 'work', 地址: 'work.example.com', 登录用户: 'tunnel', 私钥: '/k/id', ...over }
  for (const [label, val] of Object.entries(v)) {
    const el = screen.getByLabelText(label)
    await user.clear(el)
    if (val) await user.type(el, val)
  }
}

const saveBtn = () => screen.getByRole('button', { name: '保存' }) as HTMLButtonElement

describe('Server dialog', () => {
  beforeEach(() => {
    resetApiMock()
    setLang('zh')
  })

  it('shows live name errors and disables Save', async () => {
    const { user } = await open()
    await fillAdd(user, { 名称: '-bad' })
    expect(screen.getByText(/名字只能用字母/)).toBeTruthy()
    expect(saveBtn().disabled).toBe(true)
    await fillAdd(user, { 名称: 'home' })
    expect(screen.getByText('名称「home」已存在')).toBeTruthy()
    await fillAdd(user, { 名称: 'Home' }) // case-sensitive
    expect(screen.queryByText(/已存在/)).toBeNull()
    expect(screen.queryByText(/名字只能用字母/)).toBeNull()
  })

  it('validates the port as an integer in 1-65535', async () => {
    const { user } = await open()
    await fillAdd(user)
    expect((screen.getByLabelText('SSH 端口') as HTMLInputElement).value).toBe('22')
    await user.clear(screen.getByLabelText('SSH 端口'))
    await user.type(screen.getByLabelText('SSH 端口'), '70000')
    expect(screen.getByText('端口需在 1–65535 之间')).toBeTruthy()
    expect((screen.getByRole('button', { name: '测试连接' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('test success shows fingerprint + check command; Save needs trust', async () => {
    apiMock.TestConnection.mockResolvedValue(probeOk)
    const { user } = await open()
    await fillAdd(user)
    expect(saveBtn().disabled).toBe(true)
    await user.click(screen.getByRole('button', { name: '测试连接' }))
    expect(apiMock.TestConnection).toHaveBeenCalledWith({ host: 'work.example.com', port: 22, user: 'tunnel', key_path: '/k/id', socket_dir: '' })
    expect(await screen.findByText(FP)).toBeTruthy()
    expect(screen.getByText('SHA256 指纹')).toBeTruthy()
    expect(screen.getByText('在服务器上运行以下命令核对：')).toBeTruthy()
    expect(screen.getByText('ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub')).toBeTruthy()
    expect(saveBtn().disabled).toBe(true)
    await user.click(screen.getByRole('button', { name: '指纹一致,信任' }))
    expect(saveBtn().disabled).toBe(false)
  })

  it('the test button is disabled and says 正在测试… while running', async () => {
    let resolve!: (v: typeof probeOk) => void
    apiMock.TestConnection.mockImplementation(() => new Promise((r) => (resolve = r)) as any)
    const { user } = await open()
    await fillAdd(user)
    await user.click(screen.getByRole('button', { name: '测试连接' }))
    const b = screen.getByRole('button', { name: '正在测试…' }) as HTMLButtonElement
    expect(b.disabled).toBe(true)
    resolve(probeOk)
    expect(await screen.findByText(FP)).toBeTruthy()
  })

  const errCases: [string, string, string][] = [
    [
      'passphrase: x',
      'passphrase',
      'Tunnel 需要一把没有口令的私钥。可以为它单独生成一把:ssh-keygen -t ed25519 -N "" -f ~/.ssh/tunnel_ed25519,并把公钥加到服务器的 ~/.ssh/authorized_keys',
    ],
    ['auth: denied', 'auth', '服务器拒绝了这把私钥，请确认公钥已加入服务器的 ~/.ssh/authorized_keys'],
    ['dial: connection refused', 'dial', '连不上 work.example.com:22：connection refused'],
    ['socketdir: permission denied', 'socketdir', '已登录，但无法创建 socket 目录：permission denied'],
    ['boom: weird', '', 'boom: weird'],
  ]
  it.each(errCases)('test error %s -> sentence', async (msg, _code, text) => {
    apiMock.TestConnection.mockRejectedValue(new Error(msg))
    const { user } = await open()
    await fillAdd(user)
    await user.click(screen.getByRole('button', { name: '测试连接' }))
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe(text)
    expect(saveBtn().disabled).toBe(true)
  })

  it('editing: unchanged keeps Save enabled; renaming only does not require a test', async () => {
    const { user } = await open(base(), 'edit')
    expect((screen.getByLabelText('名称') as HTMLInputElement).value).toBe('home')
    expect(saveBtn().disabled).toBe(false)
    await user.clear(screen.getByLabelText('名称'))
    await user.type(screen.getByLabelText('名称'), 'home2')
    expect(saveBtn().disabled).toBe(false)
    await user.click(saveBtn())
    expect(apiMock.UpdateServer).toHaveBeenCalledTimes(1)
    const [old, sv] = apiMock.UpdateServer.mock.calls[0]
    expect(old).toBe('home')
    expect(sv).toMatchObject({ name: 'home2', host: 'vps.example.com', port: 22, user: 'tunnel', key_path: '/home/u/.ssh/id_ed25519', host_key: 'ssh-ed25519 AAAAhome', socket_dir: '' })
    expect(apiMock.Select).not.toHaveBeenCalled()
  })

  it.each([
    ['地址', 'other.example.com'],
    ['SSH 端口', '2222'],
    ['登录用户', 'bob'],
    ['私钥', '/k/other'],
  ])('editing: changing %s requires a new test and trust', async (label, val) => {
    apiMock.TestConnection.mockResolvedValue(probeOk)
    const { user } = await open(base(), 'edit')
    await user.clear(screen.getByLabelText(label))
    await user.type(screen.getByLabelText(label), val)
    expect(saveBtn().disabled).toBe(true)
    await user.click(screen.getByRole('button', { name: '测试连接' }))
    await screen.findByText(FP)
    expect(saveBtn().disabled).toBe(true)
    await user.click(screen.getByRole('button', { name: '指纹一致,信任' }))
    expect(saveBtn().disabled).toBe(false)
    await user.click(saveBtn())
    expect(apiMock.UpdateServer.mock.calls[0][1]).toMatchObject({ host_key: 'ssh-ed25519 AAAAnew' })
  })

  it('changing a field after trust clears the trust', async () => {
    apiMock.TestConnection.mockResolvedValue(probeOk)
    const { user } = await open()
    await fillAdd(user)
    await user.click(screen.getByRole('button', { name: '测试连接' }))
    await user.click(await screen.findByRole('button', { name: '指纹一致,信任' }))
    expect(saveBtn().disabled).toBe(false)
    await user.type(screen.getByLabelText('登录用户'), 'x')
    expect(saveBtn().disabled).toBe(true)
    expect(screen.queryByText(FP)).toBeNull()
  })

  it('advanced: socket dir collapsed by default, empty with 自动 placeholder', async () => {
    const { user } = await open()
    expect(screen.queryByLabelText('socket 目录')).toBeNull()
    await user.click(screen.getByRole('button', { name: '高级' }))
    const el = screen.getByLabelText('socket 目录') as HTMLInputElement
    expect(el.value).toBe('')
    expect(el.placeholder).toBe('自动')
  })

  it('pick key fills the field; cancel keeps the old value', async () => {
    const { user } = await open()
    await fillAdd(user)
    apiMock.PickKeyFile.mockResolvedValueOnce('/picked/id')
    await user.click(screen.getByRole('button', { name: '选择…' }))
    await waitFor(() => expect((screen.getByLabelText('私钥') as HTMLInputElement).value).toBe('/picked/id'))
    apiMock.PickKeyFile.mockResolvedValueOnce('')
    await user.click(screen.getByRole('button', { name: '选择…' }))
    await waitFor(() => expect(apiMock.PickKeyFile).toHaveBeenCalledTimes(2))
    expect((screen.getByLabelText('私钥') as HTMLInputElement).value).toBe('/picked/id')
  })

  it('add: AddServer then Select(new name)', async () => {
    apiMock.TestConnection.mockResolvedValue(probeOk)
    const { user } = await open()
    await fillAdd(user)
    await user.click(screen.getByRole('button', { name: '测试连接' }))
    await user.click(await screen.findByRole('button', { name: '指纹一致,信任' }))
    await user.click(saveBtn())
    expect(apiMock.AddServer).toHaveBeenCalledTimes(1)
    expect(apiMock.AddServer.mock.calls[0][0]).toMatchObject({ name: 'work', host: 'work.example.com', port: 22, user: 'tunnel', key_path: '/k/id', host_key: 'ssh-ed25519 AAAAnew', socket_dir: '' })
    await waitFor(() => expect(apiMock.Select).toHaveBeenCalledWith('work'))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('save errors show at the bottom and keep the dialog open', async () => {
    apiMock.TestConnection.mockResolvedValue(probeOk)
    apiMock.AddServer.mockRejectedValue(new Error('disk full'))
    const { user } = await open()
    await fillAdd(user)
    await user.click(screen.getByRole('button', { name: '测试连接' }))
    await user.click(await screen.findByRole('button', { name: '指纹一致,信任' }))
    await user.click(saveBtn())
    expect((await screen.findByText('disk full')).closest('.dlg-f')).toBeTruthy()
    expect(apiMock.Select).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeTruthy()
  })

  it('empty-state main button opens the add dialog', async () => {
    apiMock.GetState.mockResolvedValue(appState())
    render(<App />)
    const user = userEvent.setup()
    const main = (await screen.findByRole('heading', { name: '添加第一台服务器' })).parentElement!
    await user.click(within(main).getByRole('button', { name: '添加服务器' }))
    expect(screen.getByRole('dialog')).toBeTruthy()
  })

  it('a state event does not remount the dialog or reset what was typed', async () => {
    const { user, dlg } = await open()
    await fillAdd(user)
    pushState({ ...base(), servers: [server({ connected: 2 })] })
    expect(screen.getByRole('dialog')).toBe(dlg)
    expect((screen.getByLabelText('名称') as HTMLInputElement).value).toBe('work')
  })

  it('a test that finishes after the host changed is ignored', async () => {
    let resolve!: (v: typeof probeOk) => void
    apiMock.TestConnection.mockImplementation(() => new Promise((r) => (resolve = r)) as any)
    const { user } = await open()
    await fillAdd(user)
    await user.click(screen.getByRole('button', { name: '测试连接' }))
    expect(screen.getByRole('button', { name: '正在测试…' })).toBeTruthy()
    await user.type(screen.getByLabelText('地址'), 'x')
    // the stale test no longer blocks the button
    expect((screen.getByRole('button', { name: '测试连接' }) as HTMLButtonElement).disabled).toBe(false)
    await act(async () => resolve(probeOk))
    expect(screen.queryByText(FP)).toBeNull()
    expect(screen.queryByRole('button', { name: '指纹一致,信任' })).toBeNull()
    expect(screen.queryByRole('button', { name: '正在测试…' })).toBeNull()
    expect(saveBtn().disabled).toBe(true)
  })

  it('a late failure of a stale test shows no error either', async () => {
    let reject!: (e: Error) => void
    apiMock.TestConnection.mockImplementation(() => new Promise((_, r) => (reject = r)) as any)
    const { user } = await open()
    await fillAdd(user)
    await user.click(screen.getByRole('button', { name: '测试连接' }))
    await user.type(screen.getByLabelText('地址'), 'x')
    await act(async () => reject(new Error('auth: nope')))
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('editing: changing the socket directory drops trust', async () => {
    const { user } = await open(base(), 'edit')
    expect(saveBtn().disabled).toBe(false)
    await user.click(screen.getByRole('button', { name: '高级' }))
    await user.type(screen.getByLabelText('socket 目录'), '/run/x')
    expect(saveBtn().disabled).toBe(true)
  })

  it('editing: picking a different key drops trust', async () => {
    apiMock.PickKeyFile.mockResolvedValueOnce('/picked/other')
    const { user } = await open(base(), 'edit')
    expect(saveBtn().disabled).toBe(false)
    await user.click(screen.getByRole('button', { name: '选择…' }))
    await waitFor(() => expect((screen.getByLabelText('私钥') as HTMLInputElement).value).toBe('/picked/other'))
    expect(saveBtn().disabled).toBe(true)
  })

  it('a failing key picker shows its message under the key field', async () => {
    apiMock.PickKeyFile.mockRejectedValueOnce(new Error('dialog unavailable'))
    const { user } = await open()
    await fillAdd(user)
    await user.click(screen.getByRole('button', { name: '选择…' }))
    const msg = await screen.findByText('dialog unavailable')
    expect(msg.className).toContain('fld-err')
    expect((screen.getByLabelText('私钥') as HTMLInputElement).value).toBe('/k/id')
    await user.type(screen.getByLabelText('私钥'), 'x')
    expect(screen.queryByText('dialog unavailable')).toBeNull()
  })
})
