import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { apiMock, logListenerCount, pushLog, resetApiMock } from './apiMock'
import { terms } from './xtermMock'
import { appState } from './fixtures'
import { setLang } from '../i18n'
import LogViewer, { fontSize, FONT_DEFAULT, FONT_MAX, FONT_MIN } from '../components/LogViewer'

vi.mock('../api', async () => (await import('./apiMock')).apiMock)

const settings = appState().settings
const last = () => terms[terms.length - 1]

async function open(extra: Partial<typeof settings> = {}, onClose = () => {}) {
  const r = render(<LogViewer server="home" settings={{ ...settings, ...extra }} onClose={onClose} />)
  await act(async () => {}) // StartLog resolves
  return r
}

describe('LogViewer', () => {
  beforeEach(() => {
    resetApiMock()
    setLang('zh')
    terms.length = 0
  })

  it('starts the stream, writes its chunks read-only and shows it is live', async () => {
    await open()
    expect(apiMock.StartLog).toHaveBeenCalledWith('home')
    const term = last()
    expect(term.opened).toBe(true)
    expect(term.options.disableStdin).toBe(true)
    expect(term.options.convertEol).toBe(true)
    // Esc is left to the dialog, other keys to the terminal (selection, copy)
    expect(term.keyHandler!(new KeyboardEvent('keydown', { key: 'Escape' }))).toBe(false)
    expect(term.keyHandler!(new KeyboardEvent('keydown', { key: 'c', metaKey: true }))).toBe(true)
    pushLog({ id: 1, data: 'line 1\n' })
    pushLog({ id: 7, data: 'other stream\n' }) // not ours
    pushLog({ id: 1, data: 'line 2\n' })
    expect(term.written).toBe('line 1\nline 2\n')
    expect(screen.getByText('实时更新中')).toBeTruthy()
    pushLog({ id: 1, done: true })
    expect(screen.getByText('日志已停止更新')).toBeTruthy()
  })

  it('keeps chunks that arrive before StartLog resolves', async () => {
    let resolve!: (id: number) => void
    apiMock.StartLog.mockImplementationOnce(() => new Promise<number>((r) => (resolve = r)))
    render(<LogViewer server="home" settings={settings} onClose={() => {}} />)
    pushLog({ id: 3, data: 'early\n' })
    await act(async () => resolve(3))
    pushLog({ id: 3, data: 'late\n' })
    expect(last().written).toBe('early\nlate\n')
  })

  it('stops the stream and disposes the terminal on close', async () => {
    const { unmount } = await open()
    const term = last()
    unmount()
    expect(apiMock.StopLog).toHaveBeenCalledWith(1)
    expect(term.disposed).toBe(true)
    expect(logListenerCount()).toBe(0)
  })

  it('missing log file and stream errors are shown in the footer', async () => {
    apiMock.StartLog.mockRejectedValueOnce(new Error('nolog: /x/tunnel.log'))
    await open()
    expect(screen.getByText('日志文件还不存在')).toBeTruthy()
  })

  it('stream failure shows its error', async () => {
    await open()
    pushLog({ id: 1, done: true, err: 'permission denied' })
    expect(screen.getByText('读取日志失败：permission denied')).toBeTruthy()
  })

  it('font size: starts from the setting, A+/A− change it live and save it', async () => {
    const u = userEvent.setup()
    await open({ log_font_size: 14 })
    const term = last()
    expect(term.options.fontSize).toBe(14)
    await u.click(screen.getByRole('button', { name: '放大字号' }))
    expect(term.options.fontSize).toBe(15)
    expect(apiMock.UpdateSettings).toHaveBeenLastCalledWith(expect.objectContaining({ log_font_size: 15 }))
    await u.click(screen.getByRole('button', { name: '缩小字号' }))
    await u.click(screen.getByRole('button', { name: '缩小字号' }))
    expect(term.options.fontSize).toBe(13)
  })

  it('font size is clamped and defaults when unset', () => {
    expect(fontSize(0)).toBe(FONT_DEFAULT)
    expect(fontSize(undefined)).toBe(FONT_DEFAULT)
    expect(fontSize(3)).toBe(FONT_MIN)
    expect(fontSize(99)).toBe(FONT_MAX)
  })

  it('search: Enter finds next, Shift+Enter previous', async () => {
    const u = userEvent.setup()
    await open()
    const search = last().addons.find((a) => 'findNext' in a)
    await u.type(screen.getByRole('searchbox', { name: '搜索日志' }), 'error{Enter}')
    expect(search.findNext).toHaveBeenCalledWith('error')
    await u.keyboard('{Shift>}{Enter}{/Shift}')
    expect(search.findPrevious).toHaveBeenCalledWith('error')
  })

  it('Enter in the search box does not close the dialog; Esc does', async () => {
    const u = userEvent.setup()
    const onClose = vi.fn()
    await open({}, onClose)
    await u.type(screen.getByRole('searchbox', { name: '搜索日志' }), 'x{Enter}')
    expect(onClose).not.toHaveBeenCalled()
    await u.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalled()
  })

  it('clear empties the terminal; copy all copies everything', async () => {
    const u = userEvent.setup()
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    await open()
    pushLog({ id: 1, data: 'a\nb\n' })
    await u.click(screen.getByRole('button', { name: '复制全部' }))
    expect(writeText).toHaveBeenCalledWith('a\nb\n')
    expect(await screen.findByText('已复制')).toBeTruthy()
    await u.click(screen.getByRole('button', { name: '清屏' }))
    expect(last().written).toBe('')
  })

  it('theme follows the app: colours come from the CSS tokens and update on mode change', async () => {
    document.documentElement.style.setProperty('--surface', '#101010')
    await open()
    const term = last()
    expect(term.options.theme.background).toBe('#101010')
    document.documentElement.style.setProperty('--surface', '#fafafa')
    await act(async () => {
      document.documentElement.classList.add('dark')
    })
    expect(term.options.theme.background).toBe('#fafafa')
    document.documentElement.classList.remove('dark')
    document.documentElement.style.removeProperty('--surface')
  })
})
