import React, { forwardRef } from 'react'
import { IconButton as ChakraIconButton, Tooltip } from '@chakra-ui/react'
import CustomIcon from '../Icons/CustomIcon'
import styles from './styles.module.scss'

const RMIconButton = forwardRef(
  ({
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
    },
    ref
  ) => {
  return tooltip ? (
    <Tooltip as={"div"} label={tooltip}>
      <ChakraIconButton
        ref={ref}
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
      ref={ref}
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
})

export default RMIconButton
