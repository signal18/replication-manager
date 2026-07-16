import React from 'react'
import { Button as ChakraButton } from '@chakra-ui/react'
import styles from './styles.module.scss'

// Chakra's own size scale ('sm'/'md'/'lg'/'xs') leaks in from call sites
// copy-pasted from plain Chakra buttons, but this component's CSS classes
// are keyed by 'small'/'medium'/'large'/'xs'. A mismatch silently drops the
// sizing class entirely, falling back to Chakra's default 'md' button
// (40px) - larger than any size this component actually offers.
const SIZE_ALIASES = { sm: 'small', md: 'medium', lg: 'large' }

function RMButton({
  children,
  onClick,
  className,
  colorScheme,
  variant,
  type = 'button',
  size = 'small',
  isBlinking,
  ...rest
}) {
  const resolvedSize = SIZE_ALIASES[size] || size
  return (
    <ChakraButton
      className={`${styles.button} ${variant || colorScheme ? '' : styles.defaultColor} ${styles[resolvedSize]} ${isBlinking && styles.blinking} ${className}`}
      colorScheme={colorScheme}
      variant={variant}
      type={type}
      onClick={onClick}
      {...rest}>
      {children}
    </ChakraButton>
  )
}

export default RMButton
