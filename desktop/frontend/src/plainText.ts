/**
 * Props for text inputs holding names, hosts, user names, ports and paths:
 * no automatic capitalisation (macOS "Capitalise words automatically" also
 * applies to WebKit inputs), autocorrect, spellcheck or autofill.
 */
export const plainText = {
  autoCapitalize: 'off',
  autoCorrect: 'off',
  autoComplete: 'off',
  spellCheck: false,
} as const
