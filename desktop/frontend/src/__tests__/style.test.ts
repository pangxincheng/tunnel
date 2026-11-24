import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// The accent colour is only for online/connected, switch on, live port, links and focus.
const css = readFileSync(resolve(__dirname, '../styles/app.css'), 'utf8')
const rule = (sel: string) => {
  const m = css.match(new RegExp(`(^|\\n)${sel.replace(/[.[\]()*+?^$|\\]/g, '\\$&')}\\s*\\{([^}]*)\\}`))
  if (!m) throw new Error('missing rule ' + sel)
  return m[2]
}

describe('accent usage', () => {
  it.each(['.errbox.info', '.ph.user', '.tick'])('%s does not use the accent', (sel) => {
    expect(rule(sel)).not.toMatch(/--live/)
  })
})

describe('macOS title bar', () => {
  // The window uses a hidden-inset title bar: the traffic lights are drawn over
  // the top 46px of the web view, so dialogs must start below that strip.
  it('dialog scrim keeps clear of the traffic lights', () => {
    expect(rule('.app[data-platform="darwin"] .scrim')).toMatch(/padding-top:\s*46px/)
  })
})
