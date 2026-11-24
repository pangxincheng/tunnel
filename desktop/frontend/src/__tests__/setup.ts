import { afterEach, vi } from 'vitest'
import { cleanup } from '@testing-library/react'

afterEach(() => cleanup())

// xterm.js needs a canvas and real layout; tests use recording fakes.
vi.mock('@xterm/xterm', async () => ({ Terminal: (await import('./xtermMock')).FakeTerminal }))
vi.mock('@xterm/addon-fit', async () => ({ FitAddon: (await import('./xtermMock')).FakeFit }))
vi.mock('@xterm/addon-search', async () => ({ SearchAddon: (await import('./xtermMock')).FakeSearch }))
vi.mock('@xterm/xterm/css/xterm.css', () => ({}))
