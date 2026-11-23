import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
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

  it('only the top dialog of a stack handles Esc / Enter', async () => {
    const outerCancel = vi.fn()
    const innerCancel = vi.fn()
    const innerSubmit = vi.fn()
    const outerSubmit = vi.fn()
    const tree = (inner: boolean) => (
      <>
        <Dialog title="outer" onCancel={outerCancel} onSubmit={outerSubmit}>
          <p>o</p>
        </Dialog>
        {inner && (
          <Dialog title="inner" onCancel={innerCancel} onSubmit={innerSubmit}>
            <p>i</p>
          </Dialog>
        )}
      </>
    )
    const { rerender } = render(tree(false))
    rerender(tree(true))
    const user = userEvent.setup()
    await user.keyboard('{Enter}')
    expect(innerSubmit).toHaveBeenCalledTimes(1)
    expect(outerSubmit).not.toHaveBeenCalled()
    await user.keyboard('{Escape}')
    expect(innerCancel).toHaveBeenCalledTimes(1)
    expect(outerCancel).not.toHaveBeenCalled()
    rerender(tree(false))
    await user.keyboard('{Escape}')
    expect(outerCancel).toHaveBeenCalledTimes(1)
  })

  it('a drag that starts inside the dialog and ends on the scrim does not cancel', () => {
    const onCancel = vi.fn()
    const { container } = render(
      <Dialog title="T" onCancel={onCancel} onSubmit={() => {}}>
        <p>body</p>
      </Dialog>,
    )
    const scrim = container.querySelector('.scrim')!
    fireEvent.mouseDown(screen.getByText('body'))
    fireEvent.mouseUp(scrim)
    fireEvent.click(scrim)
    expect(onCancel).not.toHaveBeenCalled()
    fireEvent.mouseDown(scrim)
    fireEvent.mouseUp(scrim)
    fireEvent.click(scrim)
    expect(onCancel).toHaveBeenCalledTimes(1)
  })
})
