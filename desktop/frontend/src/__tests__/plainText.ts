import { expect } from 'vitest'

/** Asserts that a text input has macOS / WebKit auto-capitalisation, autocorrect, spellcheck and autofill off. */
export function expectPlainText(el: Element) {
  expect(el.getAttribute('autocapitalize'), `${el.id || el.outerHTML} autocapitalize`).toBe('off')
  expect(el.getAttribute('autocorrect'), `${el.id} autocorrect`).toBe('off')
  expect(el.getAttribute('spellcheck'), `${el.id} spellcheck`).toBe('false')
  expect(el.getAttribute('autocomplete'), `${el.id} autocomplete`).toBe('off')
}
