import { useRef, useState, type ReactNode } from 'react'
import {
  AddServer,
  PickKeyFile,
  Select,
  TestConnection,
  UpdateServer,
  errorCode,
  errorDetail,
  errorMessage,
  type Server,
  type ServerView,
} from '../api'
import { t } from '../i18n'
import { useCopy } from '../clipboard'
import Dialog from './Dialog'

const NAME_RE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/
const CHECK_CMD = 'ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub'

interface Props {
  /** Every server (to keep names unique). */
  servers: ServerView[]
  /** Set when editing; the form is initialised from this snapshot only. */
  editing?: ServerView
  onClose: () => void
}

interface Probe {
  fingerprint: string
  hostKey: string
}

function parsePort(s: string): number | null {
  if (!/^\d+$/.test(s.trim())) return null
  const n = Number(s)
  return n >= 1 && n <= 65535 ? n : null
}

function Field({ id, label, error, children }: { id: string; label: string; error?: string; children: ReactNode }) {
  return (
    <>
      <div className="field">
        <label htmlFor={id}>{label}</label>
        <div className="ctl">{children}</div>
      </div>
      {error && <div className="fld-err">{error}</div>}
    </>
  )
}

export default function ServerDialog({ servers, editing, onClose }: Props) {
  const initial = useRef(editing)
  const ed = initial.current
  const [name, setName] = useState(ed?.name ?? '')
  const [host, setHost] = useState(ed?.host ?? '')
  const [port, setPort] = useState(String(ed?.port ?? 22))
  const [user, setUser] = useState(ed?.user ?? '')
  const [keyPath, setKeyPath] = useState(ed?.key_path ?? '')
  const [socketDir, setSocketDir] = useState(ed?.socket_dir ?? '')
  const [advanced, setAdvanced] = useState(false)

  const [probe, setProbe] = useState<Probe | null>(null)
  // Editing starts out trusted: the stored host key stays valid until a connection setting changes.
  const [trusted, setTrusted] = useState(!!ed)
  const [testing, setTesting] = useState(false)
  const [testErr, setTestErr] = useState('')
  const [saving, setSaving] = useState(false)
  const [saveErr, setSaveErr] = useState('')
  const [pickErr, setPickErr] = useState('')
  const testSeq = useRef(0)
  const nameRef = useRef<HTMLInputElement>(null)
  const hostRef = useRef<HTMLInputElement>(null)
  const [copied, copy] = useCopy()

  const portNum = parsePort(port)
  const nameBad = name !== '' && !NAME_RE.test(name)
  const nameDup = !nameBad && servers.some((s) => s.name === name && s.name !== ed?.name)
  const nameErr = nameBad ? t('nameInvalid') : nameDup ? t('serverNameTaken', { name }) : ''
  const portErr = port !== '' && portNum === null ? t('portRange') : ''
  const connValid = host.trim() !== '' && portNum !== null && user.trim() !== '' && keyPath.trim() !== ''
  const canSave = NAME_RE.test(name) && !nameDup && connValid && trusted && !saving

  // Editing a connection setting drops the trust (and any test in flight).
  const conn = (set: (v: string) => void) => (v: string) => {
    set(v)
    testSeq.current++
    setTesting(false)
    setProbe(null)
    setTrusted(false)
    setTestErr('')
  }

  async function pickKey() {
    setPickErr('')
    try {
      const p = await PickKeyFile()
      if (p) conn(setKeyPath)(p)
    } catch (e) {
      setPickErr(errorMessage(e))
    }
  }

  function describe(e: unknown): string {
    switch (errorCode(e)) {
      case 'passphrase':
        return t('errPassphrase')
      case 'auth':
        return t('errAuth')
      case 'dial':
        return t('errDial', { host: host.trim(), port: portNum ?? port, detail: errorDetail(e) })
      case 'socketdir':
        return t('errSocketDir', { detail: errorDetail(e) })
      default:
        return errorMessage(e)
    }
  }

  async function runTest() {
    if (!connValid || portNum === null) return
    const seq = ++testSeq.current
    setTesting(true)
    setTestErr('')
    setProbe(null)
    setTrusted(false)
    try {
      const r = await TestConnection({ host: host.trim(), port: portNum, user: user.trim(), key_path: keyPath.trim(), socket_dir: socketDir.trim() })
      if (seq !== testSeq.current) return
      setProbe({ fingerprint: r.fingerprint, hostKey: r.host_key })
    } catch (e) {
      if (seq !== testSeq.current) return
      setTestErr(describe(e))
    } finally {
      if (seq === testSeq.current) setTesting(false)
    }
  }

  async function save() {
    if (!canSave || portNum === null) return
    const sv: Server = {
      name,
      host: host.trim(),
      port: portNum,
      user: user.trim(),
      key_path: keyPath.trim(),
      host_key: probe?.hostKey ?? ed?.host_key ?? '',
      socket_dir: socketDir.trim(),
      // The backend keeps these from the stored server on update.
      disconnected: false,
      share: { on: false, name: '', system: false },
      forwards: null,
      last_seen: null,
      devices: null,
      devices_at: '0001-01-01T00:00:00Z',
    }
    setSaving(true)
    setSaveErr('')
    try {
      if (ed) {
        await UpdateServer(ed.name, sv)
      } else {
        await AddServer(sv)
        await Select(name)
      }
      onClose()
    } catch (e) {
      setSaveErr(errorMessage(e))
      setSaving(false)
    }
  }

  return (
    <Dialog
      title={ed ? t('serverSettings') : t('addServer')}
      subtitle={ed?.name}
      onCancel={onClose}
      onSubmit={() => void save()}
      initialFocus={ed ? hostRef : nameRef}
      footer={
        <>
          {saveErr ? (
            <span className="fld-err left" role="alert">
              {saveErr}
            </span>
          ) : (
            !trusted && <span className="hint left">{t('needTrust')}</span>
          )}
          <button type="button" className="btn plain" onClick={onClose}>
            {t('cancel')}
          </button>
          <button type="button" className="btn plain" disabled={!connValid || testing} onClick={() => void runTest()}>
            {testing ? t('testingConn') : t('testConn')}
          </button>
          <button type="button" className="btn primary" disabled={!canSave} onClick={() => void save()}>
            {t('save')}
          </button>
        </>
      }
    >
      <div className="list form">
        <Field id="sd-name" label={t('fieldName')}>
          <input id="sd-name" ref={nameRef} className={nameErr ? 'inp bad' : 'inp'} value={name} aria-invalid={!!nameErr} onChange={(e) => setName(e.target.value)} />
        </Field>
        {nameErr && <div className="fld-err">{nameErr}</div>}
        <Field id="sd-host" label={t('fieldHost')}>
          <input id="sd-host" ref={hostRef} className="inp" value={host} onChange={(e) => conn(setHost)(e.target.value)} />
        </Field>
        <Field id="sd-port" label={t('fieldPort')}>
          <input id="sd-port" className={portErr ? 'inp bad' : 'inp'} inputMode="numeric" value={port} aria-invalid={!!portErr} onChange={(e) => conn(setPort)(e.target.value)} />
        </Field>
        {portErr && <div className="fld-err">{portErr}</div>}
        <Field id="sd-user" label={t('fieldUser')}>
          <input id="sd-user" className="inp" value={user} onChange={(e) => conn(setUser)(e.target.value)} />
        </Field>
        <Field id="sd-key" label={t('fieldKey')}>
          <input id="sd-key" className="inp" value={keyPath} onChange={(e) => {
              setPickErr('')
              conn(setKeyPath)(e.target.value)
            }}
          />
          <button type="button" className="btn plain sm" onClick={() => void pickKey()}>
            {t('pickKey')}
          </button>
        </Field>
        {pickErr && <div className="fld-err">{pickErr}</div>}
        <button type="button" className="adv-toggle" aria-expanded={advanced} onClick={() => setAdvanced((v) => !v)}>
          {t('advanced')}
        </button>
        {advanced && (
          <Field id="sd-sock" label={t('socketDir')}>
            <input id="sd-sock" className="inp" placeholder={t('socketAuto')} value={socketDir} onChange={(e) => conn(setSocketDir)(e.target.value)} />
          </Field>
        )}
      </div>
      {testErr && (
        <div className="errbox" role="alert">
          {testErr}
        </div>
      )}
      {probe && (
        <div className="fp">
          <div className="t">{t('fpTitle')}</div>
          <code>{probe.fingerprint}</code>
          {trusted ? (
            <div className="ok">{t('trusted')}</div>
          ) : (
            <>
              <p>{t('fpHint')}</p>
              <div className="cmdline">
                <code>{CHECK_CMD}</code>
                <button type="button" className="lb" aria-label={t('copyCmd')} onClick={() => copy('chk', CHECK_CMD)}>
                  {copied?.id === 'chk' ? (copied.ok ? t('copied') : t('copyFailed')) : t('copyCmd')}
                </button>
              </div>
              <div className="fp-act">
                <button type="button" className="btn plain" onClick={() => setTrusted(true)}>
                  {t('trust')}
                </button>
              </div>
            </>
          )}
        </div>
      )}
    </Dialog>
  )
}
