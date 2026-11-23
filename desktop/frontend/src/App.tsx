import { useEffect, useState } from 'react'
import { useAppState } from './state'
import { resolveLang, setLang } from './i18n'
import Sidebar from './components/Sidebar'
import EmptyState from './components/EmptyState'
import ServerDetail from './components/ServerDetail'
import SettingsDialog from './components/SettingsDialog'

function useSystemDark(): boolean {
  const mq = typeof window.matchMedia === 'function' ? window.matchMedia('(prefers-color-scheme: dark)') : null
  const [dark, setDark] = useState(mq?.matches ?? false)
  useEffect(() => {
    if (!mq) return
    const on = () => setDark(mq.matches)
    on()
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])
  return dark
}

export default function App() {
  const state = useAppState()
  const systemDark = useSystemDark()
  const [settingsOpen, setSettingsOpen] = useState(false)

  const lang = state ? resolveLang(state.settings.lang, navigator.language) : null
  // t() reads the module-level language; set it before children render.
  if (lang) setLang(lang)
  useEffect(() => {
    if (lang) document.documentElement.lang = lang === 'zh' ? 'zh-CN' : 'en'
  }, [lang])

  if (!state) return null

  const { settings } = state
  const dark = settings.mode === 'dark' || (settings.mode === 'system' && systemDark)
  const servers = state.servers ?? []
  const current = servers.find((s) => s.name === state.selected) ?? servers[0]

  // TODO(task 11): open the add / edit server dialog and the delete confirm.
  const onAdd = () => {}
  const onEdit = (_name: string) => {}
  const onDelete = (_name: string) => {}

  return (
    <div className={dark ? 'app dark' : 'app'} data-accent={settings.accent} data-platform={state.platform}>
      <Sidebar state={state} onAdd={onAdd} onEdit={onEdit} onDelete={onDelete} onSettings={() => setSettingsOpen(true)} />
      <section className="main">
        {state.platform === 'darwin' && !current && <div className="drag-strip" />}
        {current ? <ServerDetail server={current} /> : <EmptyState onAdd={onAdd} />}
      </section>
      {settingsOpen && <SettingsDialog settings={settings} onClose={() => setSettingsOpen(false)} />}
    </div>
  )
}
