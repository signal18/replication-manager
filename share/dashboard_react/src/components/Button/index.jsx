import React from 'react'
import { Button as ChakraButton } from '@chakra-ui/react'
import styles from './styles.module.scss'

function Button({
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
  return (
    <ChakraButton
      className={`${styles.button} ${variant || colorScheme ? '' : styles.defaultColor} ${styles[size]} ${isBlinking && styles.blinking} ${className}`}
      colorScheme={colorScheme}
      variant={variant}
      type={type}
      onClick={onClick}
      {...rest}>
      {children}
    </ChakraButton>
  )
}

export default Button
