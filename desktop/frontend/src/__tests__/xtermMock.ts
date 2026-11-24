import { vi } from 'vitest'

/** Records what LogViewer does with its terminal (xterm.js needs a real canvas / layout). */
export const terms: FakeTerminal[] = []

export class FakeTerminal {
  options: Record<string, any>
  written = ''
  disposed = false
  opened = false
  addons: any[] = []
  selection = ''
  constructor(opts: Record<string, any>) {
    this.options = { ...opts }
    terms.push(this)
  }
  keyHandler: ((e: KeyboardEvent) => boolean) | null = null
  attachCustomKeyEventHandler(f: (e: KeyboardEvent) => boolean) {
    this.keyHandler = f
  }
  loadAddon(a: any) {
    this.addons.push(a)
  }
  open() {
    this.opened = true
  }
  write(s: string) {
    this.written += s
  }
  clear() {
    this.written = ''
  }
  selectAll() {
    this.selection = this.written
  }
  getSelection() {
    return this.selection
  }
  clearSelection() {
    this.selection = ''
  }
  dispose() {
    this.disposed = true
  }
}

export class FakeFit {
  fit = vi.fn()
}

export class FakeSearch {
  findNext = vi.fn()
  findPrevious = vi.fn()
}
