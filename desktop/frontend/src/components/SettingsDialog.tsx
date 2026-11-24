import { useEffect, useRef, useState } from 'react'
import { AppVersion, UpdateSettings, errorCode, errorDetail, type Settings } from '../api'
import { t, type MsgKey } from '../i18n'
import Dialog from './Dialog'
import Switch from './Switch'

// Swatch colours live in tokens.css (per theme), keyed by data-accent.
const ACCENTS: { id: string; label: MsgKey }[] = [
  { id: 'green', label: 'accentGreen' },
  { id: 'blue', label: 'accentBlue' },
  { id: 'violet', label: 'accentViolet' },
  { id: 'amber', label: 'accentAmber' },
  { id: 'graphite', label: 'accentGraphite' },
]

function Seg({ value, options, onPick }: { value: string; options: [string, string][]; onPick: (v: string) => void }) {
  return (
    <span className="seg">
      {options.map(([v, label]) => (
        <button key={v} type="button" className={v === value ? 'sel' : ''} aria-pressed={v === value} onClick={() => onPick(v)}>
          {label}
        </button>
      ))}
    </span>
  )
}

/** The dialog keeps no copy of the settings: every control reflects the `settings` prop. */
export default function SettingsDialog({ settings, onClose }: { settings: Settings; onClose: () => void }) {
  const [version, setVersion] = useState('')
  const [err, setErr] = useState('')
  const doneRef = useRef<HTMLButtonElement>(null)
  // Latest settings sent or received: patches merge into this, so two quick
  // changes before the next state event don't revert each other.
  const latest = useRef(settings)
  const seen = useRef(settings)
  if (seen.current !== settings) {
    seen.current = settings
    latest.current = settings
  }

  useEffect(() => {
    let live = true
    AppVersion()
      .then((v) => live && setVersion(v))
      .catch(() => {})
    return () => {
      live = false
    }
  }, [])

  const update = (patch: Partial<Settings>) => {
    const next = { ...latest.current, ...patch }
    latest.current = next
    UpdateSettings(next)
      .then(() => setErr(''))
      .catch((e) =>
        setErr(
          errorCode(e) === 'autostart'
            ? t('autostartFailed', { detail: errorDetail(e) })
            : t('settingsFailed', { detail: errorDetail(e) }),
        ),
      )
  }

  return (
    <Dialog
      title={t('settings')}
      onCancel={onClose}
      onSubmit={onClose}
      initialFocus={doneRef}
      footer={
        <button ref={doneRef} type="button" className="btn primary" onClick={onClose}>
          {t('done')}
        </button>
      }
    >
      <div className="sec first">
        <div className="sec-head">
          <span className="t">{t('appearance')}</span>
        </div>
        <div className="list">
          <div className="sub flush">
            <div className="frow">
              <span className="lbl">{t('mode')}</span>
              <Seg
                value={settings.mode}
                onPick={(mode) => update({ mode })}
                options={[
                  ['system', t('modeAuto')],
                  ['light', t('modeLight')],
                  ['dark', t('modeDark')],
                ]}
              />
            </div>
            <div className="frow">
              <span className="lbl">{t('accent')}</span>
              <span className="swatches">
                {ACCENTS.map((a) => (
                  <button
                    key={a.id}
                    type="button"
                    className={a.id === settings.accent ? 'swatch sel' : 'swatch'}
                    data-accent={a.id}
                    aria-label={t(a.label)}
                    aria-pressed={a.id === settings.accent}
                    onClick={() => update({ accent: a.id })}
                  />
                ))}
              </span>
            </div>
            <div className="frow">
              <span className="lbl">{t('language')}</span>
              <Seg
                value={settings.lang}
                onPick={(lang) => update({ lang })}
                options={[
                  ['system', t('langAuto')],
                  ['zh', t('langZh')],
                  ['en', t('langEn')],
                ]}
              />
            </div>
          </div>
        </div>
      </div>
      <div className="sec">
        <div className="sec-head">
          <span className="t">{t('general')}</span>
        </div>
        <div className="list">
          <div className="sub flush">
            <div className="frow">
              <span className="lbl">{t('launchAtLogin')}</span>
              <Switch
                label={t('launchAtLogin')}
                checked={settings.launch_at_login}
                onChange={(launch_at_login) => update({ launch_at_login })}
              />
            </div>
            <div className="frow">
              <span className="lbl">
                {t('restore')}
                <small>{t('restoreHint')}</small>
              </span>
              <Switch
                label={t('restore')}
                checked={settings.restore_forwards}
                onChange={(restore_forwards) => update({ restore_forwards })}
              />
            </div>
            <div className="frow">
              <span className="lbl">{t('about')}</span>
              <span className="meta ver">{/^\d/.test(version) ? `v${version}` : version}</span>
            </div>
          </div>
        </div>
        {err && (
          <p className="ferr" role="alert">
            {err}
          </p>
        )}
      </div>
    </Dialog>
  )
}
