interface Props {
  checked: boolean
  onChange: (next: boolean) => void
  label: string
  disabled?: boolean
}

export default function Switch({ checked, onChange, label, disabled }: Props) {
  return (
    <button
      type="button"
      className="sw"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
    />
  )
}
