import { useEffect, useRef, useId, type ReactNode, type RefObject } from 'react'

interface Props {
  title: string
  subtitle?: string
  onCancel: () => void
  onSubmit?: () => void
  /** Element to focus on open; defaults to the dialog itself. */
  initialFocus?: RefObject<HTMLElement | null>
  wide?: boolean
  footer?: ReactNode
  children: ReactNode
}

// Open dialogs, bottom to top; only the top one handles keys.
const stack: symbol[] = []

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

/**
 * Modal dialog. Esc / scrim click = cancel, Enter = submit (unless a button,
 * link or select has focus, which handle Enter themselves). Focus stays inside
 * and returns to the opener on unmount. The enter animation is a CSS animation
 * on the element, so it only plays on mount.
 */
export default function Dialog({ title, subtitle, onCancel, onSubmit, initialFocus, wide, footer, children }: Props) {
  const dlgRef = useRef<HTMLDivElement>(null)
  const titleId = useId()
  const downOnScrim = useRef(false)
  const cancelRef = useRef(onCancel)
  const submitRef = useRef(onSubmit)
  cancelRef.current = onCancel
  submitRef.current = onSubmit

  useEffect(() => {
    const me = Symbol('dialog')
    stack.push(me)
    const opener = document.activeElement as HTMLElement | null
    ;(initialFocus?.current ?? dlgRef.current)?.focus()

    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.isComposing || stack[stack.length - 1] !== me) return
      const dlg = dlgRef.current
      if (!dlg) return
      if (e.key === 'Escape') {
        e.preventDefault()
        cancelRef.current()
      } else if (e.key === 'Enter') {
        const target = e.target as HTMLElement
        if (target !== document.body && !dlg.contains(target)) return
        if (target.closest('button, a, select, textarea')) return
        if (!submitRef.current) return
        e.preventDefault()
        submitRef.current()
      } else if (e.key === 'Tab') {
        const items = Array.from(dlg.querySelectorAll<HTMLElement>(FOCUSABLE))
        if (items.length === 0) {
          e.preventDefault()
          return
        }
        const first = items[0]
        const last = items[items.length - 1]
        const active = document.activeElement
        if (e.shiftKey && (active === first || active === dlg || !dlg.contains(active))) {
          e.preventDefault()
          last.focus()
        } else if (!e.shiftKey && (active === last || !dlg.contains(active))) {
          e.preventDefault()
          first.focus()
        }
      }
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      const i = stack.indexOf(me)
      if (i >= 0) stack.splice(i, 1)
      if (opener && opener.isConnected) opener.focus()
    }
    // initialFocus is only read on mount
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <div
      className="scrim"
      onMouseDown={(e) => {
        downOnScrim.current = e.target === e.currentTarget
      }}
      onClick={(e) => {
        // a drag that started inside the dialog must not cancel it
        const ok = downOnScrim.current && e.target === e.currentTarget
        downOnScrim.current = false
        if (ok) cancelRef.current()
      }}
    >
      <div
        ref={dlgRef}
        className={wide ? 'dlg wide' : 'dlg'}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
      >
        <div className="dlg-h">
          <div className="t" id={titleId}>
            {title}
          </div>
          {subtitle && <div className="s">{subtitle}</div>}
        </div>
        <div className="dlg-b">{children}</div>
        {footer && <div className="dlg-f">{footer}</div>}
      </div>
    </div>
  )
}
