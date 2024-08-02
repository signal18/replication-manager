import React from 'react'
import { IconButton as ChakraIconButton, Tooltip } from '@chakra-ui/react'
import CustomIcon from '../Icons/CustomIcon'
import styles from './styles.module.scss'

function RMIconButton({
  onClick,
  size = 'sm',
  variant = 'solid',
  icon,
  iconFontsize = '1.5rem',
  iconFillColor,
  tooltip,
  style,
  className,
  colorScheme,
  ...rest
}) {
  return tooltip ? (
    <Tooltip label={tooltip}>
      <ChakraIconButton
        style={style}
        className={`${colorScheme ? '' : styles.button} ${className}`}
        onClick={onClick}
        icon={<CustomIcon icon={icon} fontSize={iconFontsize} fill={iconFillColor} />}
        size={size}
        variant={variant}
        colorScheme={colorScheme}
        {...rest}
      />
    </Tooltip>
  ) : (
    <ChakraIconButton
      style={style}
      className={`${colorScheme ? '' : styles.button} ${className}`}
      onClick={onClick}
      icon={<CustomIcon icon={icon} fontSize={iconFontsize} fill={iconFillColor} />}
      size={size}
      variant={variant}
      colorScheme={colorScheme}
      {...rest}
    />
  )
}

export default RMIconButton
