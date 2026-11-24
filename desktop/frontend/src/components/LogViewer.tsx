import { useEffect, useRef, useState } from 'react'
import { Terminal, type ITheme } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { SearchAddon } from '@xterm/addon-search'
import '@xterm/xterm/css/xterm.css'
import { StartLog, StopLog, UpdateSettings, errorCode, errorDetail, onLog, type Settings } from '../api'
import { t } from '../i18n'
import { useCopy } from '../clipboard'
import { plainText } from '../plainText'
import Dialog from './Dialog'

interface Props {
  server: string
  settings: Settings
  onClose: () => void
}

export const FONT_MIN = 10
export const FONT_MAX = 20
export const FONT_DEFAULT = 12

/** Clamps a stored font size; 0 / missing means the default. */
export function fontSize(stored: number | undefined): number {
  if (!stored) return FONT_DEFAULT
  return Math.min(FONT_MAX, Math.max(FONT_MIN, Math.round(stored)))
}

/** Terminal colours from the app's theme tokens, so the log follows light / dark mode and the accent. */
function themeFromCSS(): ITheme {
  const css = getComputedStyle(document.documentElement)
  const v = (name: string, fallback: string) => css.getPropertyValue(name).trim() || fallback
  const live = v('--live', '#13795B')
  return {
    background: v('--surface', '#FFFFFF'),
    foreground: v('--ink', '#16191D'),
    cursor: v('--surface', '#FFFFFF'), // read-only: hide the cursor
    selectionBackground: v('--sel', 'rgba(22,25,29,.07)'),
    selectionInactiveBackground: v('--hover', 'rgba(22,25,29,.05)'),
    red: v('--fault', '#B3422E'),
    green: live,
  }
}

type Status = { kind: 'loading' } | { kind: 'live' } | { kind: 'ended' } | { kind: 'error'; text: string }

/** Read-only, live view of a server's share service log, rendered with xterm.js. */
export default function LogViewer({ server, settings, onClose }: Props) {
  const hostRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const searchRef = useRef<SearchAddon | null>(null)
  const closeRef = useRef<HTMLButtonElement>(null)
  const [status, setStatus] = useState<Status>({ kind: 'loading' })
  const [query, setQuery] = useState('')
  const [size, setSize] = useState(() => fontSize(settings.log_font_size))
  const [copyResult, copy] = useCopy()

  // Terminal + log stream for the dialog's lifetime.
  useEffect(() => {
    const term = new Terminal({
      disableStdin: true,
      convertEol: true,
      cursorBlink: false,
      scrollback: 20000,
      fontFamily: getComputedStyle(document.documentElement).getPropertyValue('--code').trim() || 'monospace',
      fontSize: size,
      theme: themeFromCSS(),
    })
    // Read-only: let Esc reach the dialog instead of being sent to the terminal.
    term.attachCustomKeyEventHandler((e) => e.key !== 'Escape')
    const fit = new FitAddon()
    const search = new SearchAddon()
    term.loadAddon(fit)
    term.loadAddon(search)
    termRef.current = term
    searchRef.current = search
    if (hostRef.current) {
      term.open(hostRef.current)
      fit.fit()
    }
    const ro = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(() => fit.fit())
    if (ro && hostRef.current) ro.observe(hostRef.current)
    // The theme follows the app's mode / accent while the log is open.
    const mo = new MutationObserver(() => (term.options.theme = themeFromCSS()))
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['class', 'data-accent'] })

    let id = 0
    let closed = false
    const pending: { id: number; data: string; done: boolean; err: string }[] = []
    const apply = (c: (typeof pending)[number]) => {
      if (c.data) term.write(c.data)
      if (c.done) setStatus(c.err ? { kind: 'error', text: t('logFailed', { err: c.err }) } : { kind: 'ended' })
    }
    // Chunks can arrive before StartLog resolves with the id: keep them until then.
    const off = onLog((c) => {
      if (id === 0) pending.push(c)
      else if (c.id === id) apply(c)
    })
    StartLog(server)
      .then((got) => {
        if (closed) {
          void StopLog(got)
          return
        }
        id = got
        setStatus({ kind: 'live' })
        pending.filter((c) => c.id === got).forEach(apply)
        pending.length = 0
      })
      .catch((e) => {
        if (closed) return
        const code = errorCode(e)
        setStatus({ kind: 'error', text: code === 'nolog' ? t('logMissing') : errorDetail(e) })
      })
    return () => {
      closed = true
      off()
      if (id) void StopLog(id)
      ro?.disconnect()
      mo.disconnect()
      term.dispose()
      termRef.current = null
      searchRef.current = null
    }
    // the stream is started once per dialog; size changes are applied below
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server])

  useEffect(() => {
    const term = termRef.current
    if (term && term.options.fontSize !== size) term.options.fontSize = size
  }, [size])

  const resize = (delta: number) => {
    const next = fontSize(size + delta)
    if (next === size) return
    setSize(next)
    UpdateSettings({ ...settings, log_font_size: next }).catch(() => {})
  }

  const find = (back: boolean) => {
    const s = searchRef.current
    if (!s || !query) return
    if (back) s.findPrevious(query)
    else s.findNext(query)
  }

  const copyAll = () => {
    const term = termRef.current
    if (!term) return
    term.selectAll()
    const text = term.getSelection()
    term.clearSelection()
    copy('all', text)
  }

  let statusText = ''
  if (status.kind === 'live') statusText = t('logFollowing')
  else if (status.kind === 'ended') statusText = t('logEnded')
  else if (status.kind === 'error') statusText = status.text

  return (
    <Dialog
      title={t('logTitle', { name: server })}
      onCancel={onClose}
      initialFocus={closeRef}
      log
      footer={
        <>
          <span className={status.kind === 'error' ? 'left meta err' : 'left meta'} role="status">
            {status.kind === 'live' && <span className="dot on small" />}
            {statusText}
          </span>
          <button ref={closeRef} type="button" className="btn primary" onClick={onClose}>
            {t('close')}
          </button>
        </>
      }
    >
      <div className="logbar">
        <input
          {...plainText}
          type="search"
          className="inp"
          placeholder={t('logSearch')}
          aria-label={t('logSearch')}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
              e.preventDefault()
              e.stopPropagation()
              find(e.shiftKey)
            }
          }}
        />
        <button type="button" className="lb quiet" aria-label={t('logPrev')} title={t('logPrev')} disabled={!query} onClick={() => find(true)}>
          ↑
        </button>
        <button type="button" className="lb quiet" aria-label={t('logNext')} title={t('logNext')} disabled={!query} onClick={() => find(false)}>
          ↓
        </button>
        <span className="grow" />
        <button type="button" className="lb quiet" aria-label={t('logSmaller')} title={t('logSmaller')} disabled={size <= FONT_MIN} onClick={() => resize(-1)}>
          A−
        </button>
        <span className="meta fsize" aria-live="polite">
          {size}
        </span>
        <button type="button" className="lb quiet" aria-label={t('logLarger')} title={t('logLarger')} disabled={size >= FONT_MAX} onClick={() => resize(1)}>
          A+
        </button>
        <button type="button" className="lb quiet" onClick={() => termRef.current?.clear()}>
          {t('logClear')}
        </button>
        <button type="button" className="lb quiet" onClick={copyAll}>
          {copyResult?.id === 'all' ? (copyResult.ok ? t('copied') : t('copyFailed')) : t('logCopyAll')}
        </button>
      </div>
      <div className="logterm" ref={hostRef} data-testid="log-terminal" />
    </Dialog>
  )
}
