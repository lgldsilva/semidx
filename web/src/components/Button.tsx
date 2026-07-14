import type { ButtonHTMLAttributes } from 'react'
import { cx } from '../lib/cx'

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'link'
export type ButtonSize = 'sm' | 'md'

const VARIANT: Record<ButtonVariant, string> = {
  primary: 'border-transparent bg-accent text-accent-fg hover:opacity-90',
  secondary: 'border-border bg-surface text-fg hover:bg-surface-2',
  ghost: 'border-transparent bg-transparent text-fg hover:bg-surface-2',
  danger: 'border-transparent bg-danger text-white hover:opacity-90',
  link: 'border-transparent bg-transparent text-accent hover:underline',
}

const SIZE: Record<ButtonSize, string> = {
  sm: 'px-2.5 py-1 text-xs',
  md: 'px-4 py-2 text-sm',
}

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant
  size?: ButtonSize
}

export function Button({
  variant = 'primary',
  size = 'md',
  type = 'button',
  className,
  ...rest
}: ButtonProps) {
  return (
    <button
      type={type}
      className={cx(
        'inline-flex cursor-pointer items-center justify-center gap-1.5 rounded-md border font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-60',
        VARIANT[variant],
        SIZE[size],
        className,
      )}
      {...rest}
    />
  )
}
