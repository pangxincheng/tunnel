import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import Dialog from '../components/Dialog'

describe('Dialog', () => {
  it('Esc cancels and Enter submits', async () => {
    const onCancel = vi.fn()
    const onSubmit = vi.fn()
    render(
      <Dialog title="T" onCancel={onCancel} onSubmit={onSubmit}>
        <p>body</p>
      </Dialog>,
    )
    const user = userEvent.setup()
    await user.keyboard('{Enter}')
    expect(onSubmit).toHaveBeenCalledTimes(1)
    await user.keyboard('{Escape}')
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('clicking the scrim cancels, clicking the dialog does not', async () => {
    const onCancel = vi.fn()
    const { container } = render(
      <Dialog title="T" onCancel={onCancel} onSubmit={() => {}}>
        <p>body</p>
      </Dialog>,
    )
    const user = userEvent.setup()
    await user.click(screen.getByText('body'))
    expect(onCancel).not.toHaveBeenCalled()
    await user.click(container.querySelector('.scrim')!)
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('Enter on a focused button activates the button, not onSubmit', async () => {
    const onSubmit = vi.fn()
    const onClick = vi.fn()
    render(
      <Dialog title="T" onCancel={() => {}} onSubmit={onSubmit}>
        <button onClick={onClick}>x</button>
      </Dialog>,
    )
    const user = userEvent.setup()
    await user.tab()
    await user.keyboard('{Enter}')
    expect(onClick).toHaveBeenCalledTimes(1)
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('keeps focus inside, honours initialFocus and restores focus on close', async () => {
    const opener = document.createElement('button')
    document.body.appendChild(opener)
    opener.focus()
    const ref = { current: null as HTMLButtonElement | null }
    const { rerender } = render(
      <Dialog title="T" onCancel={() => {}} onSubmit={() => {}} initialFocus={ref}>
        <button>a</button>
        <button ref={ref}>b</button>
      </Dialog>,
    )
    expect(document.activeElement).toBe(ref.current)
    const user = userEvent.setup()
    for (let i = 0; i < 6; i++) {
      await user.tab()
      expect(screen.getByRole('dialog').contains(document.activeElement)).toBe(true)
    }
    rerender(<div />)
    expect(document.activeElement).toBe(opener)
    opener.remove()
  })
})
