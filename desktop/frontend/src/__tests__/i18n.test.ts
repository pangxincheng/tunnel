import { describe, it, expect, beforeEach } from 'vitest'
import zh from '../i18n/zh'
import en from '../i18n/en'
import { t, setLang, resolveLang } from '../i18n'

describe('i18n', () => {
  beforeEach(() => setLang('zh'))

  it('zh and en have identical key sets', () => {
    expect(Object.keys(en).sort()).toEqual(Object.keys(zh).sort())
  })

  it('formats the delete confirmation verbatim', () => {
    expect(t('deleteConfirm', { name: 'home', n: 2 })).toBe(
      '删除 home？会停止在它上面的共享和 2 个转发，并删除这台服务器在本机的配置。服务器上的数据不受影响。',
    )
  })

  it('Chinese copy uses full-width punctuation', () => {
    // Half-width , : ; ! ? touching a Chinese character, or any half-width
    // parenthesis, reads as a typo in Chinese text.
    const halfWidth = /[一-鿿][,:;!?]|[,:;!?][一-鿿]|[()]/
    const bad = Object.entries(zh).filter(([, v]) => halfWidth.test(v))
    expect(bad).toEqual([])
  })

  it('formats the server summary as one sentence', () => {
    expect(t('serverSummary', { connected: 1, online: 3 })).toBe('已连接 1\u3000在线 3')
    setLang('en')
    expect(t('serverSummary', { connected: 1, online: 3 })).toBe('1 connected · 3 online')
  })

  it('switches language', () => {
    setLang('en')
    expect(t('settings')).toBe('Settings')
  })

  it('resolves system language from navigator.language', () => {
    expect(resolveLang('system', 'zh-CN')).toBe('zh')
    expect(resolveLang('system', 'en-US')).toBe('en')
    expect(resolveLang('en', 'zh-CN')).toBe('en')
    expect(resolveLang('zh', 'en-US')).toBe('zh')
  })
})
