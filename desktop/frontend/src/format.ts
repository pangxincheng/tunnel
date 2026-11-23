import { t, type MsgKey } from './i18n'

/** Whole-unit duration in the current language: "3 天", "5 分钟", "12 秒". */
export function duration(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  const pick = (key: MsgKey, n: number) => t(key, { n })
  if (s < 60) return pick('durSec', s)
  if (s < 3600) return pick('durMin', Math.floor(s / 60))
  if (s < 86400) return pick('durHour', Math.floor(s / 3600))
  return pick('durDay', Math.floor(s / 86400))
}

/** Duration since an RFC 3339 timestamp; "" when it is missing or unparsable. */
export function since(iso: string | undefined, now = Date.now()): string {
  if (!iso) return ''
  const at = Date.parse(iso)
  if (Number.isNaN(at) || at <= 0) return ''
  return duration(now - at)
}

/** Human OS name from the device's runtime.GOOS-style value plus its version. */
export function osLabel(os: string, version: string): string {
  const base =
    os === 'darwin' ? t('osDarwin') : os === 'windows' ? t('osWindows') : os === 'linux' ? t('osLinux') : os
  return version ? `${base} ${version}` : base
}
