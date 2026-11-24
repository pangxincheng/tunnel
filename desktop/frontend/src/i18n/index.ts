import zh, { type MsgKey } from './zh'
import en from './en'

export type { MsgKey }
export type Lang = 'zh' | 'en'

const tables: Record<Lang, Record<MsgKey, string>> = { zh, en }
let current: Lang = 'zh'

export function setLang(l: Lang) {
  current = l
}

export function getLang(): Lang {
  return current
}

/** Resolves the "system" setting from a navigator.language-style tag. */
export function resolveLang(setting: string, navLang: string): Lang {
  if (setting === 'zh' || setting === 'en') return setting
  return navLang.toLowerCase().startsWith('zh') ? 'zh' : 'en'
}

export function t(key: MsgKey, vars: Record<string, string | number> = {}): string {
  return tables[current][key].replace(/\{(\w+)\}/g, (_, k) => (k in vars ? String(vars[k]) : ''))
}
