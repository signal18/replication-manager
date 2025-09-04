import React, { forwardRef, useState } from 'react'
import { IconButton as ChakraIconButton, Tooltip } from '@chakra-ui/react'
import CustomIcon from '../Icons/CustomIcon'
import ConfirmModal from '../Modals/ConfirmModal'
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
      className = '',
      colorScheme,
      isDisabled = false,
      confirm = false,
      confirmTitle = 'Are you sure?',
      confirmBody = 'Do you want to proceed with this action?',
      confirmButtonText = 'Confirm',
      cancelButtonText = 'Cancel',
      ...rest
    },
    ref
  ) => {
    const [isConfirmOpen, setIsConfirmOpen] = useState(false)

    const handleClick = (e) => {
      if (confirm) {
        setIsConfirmOpen(true)
      } else {
        onClick?.(e)
      }
    }

    const handleConfirm = () => {
      setIsConfirmOpen(false)
      onClick?.()
    }

    const button = (
      <ChakraIconButton
        ref={ref}
        style={style}
        className={`${colorScheme ? '' : styles.button} ${className}`}
        onClick={handleClick}
        icon={
          <CustomIcon
            icon={icon}
            fontSize={iconFontsize}
            fill={iconFillColor}
          />
        }
        size={size}
        variant={variant}
        colorScheme={colorScheme}
        isDisabled={isDisabled}
        {...rest}
      />
    )

    return (
      <>
        {tooltip ? (
          <Tooltip as="div" label={tooltip}>
            {button}
          </Tooltip>
        ) : (
          button
        )}

        {confirm && (
          <ConfirmModal
            isOpen={isConfirmOpen}
            closeModal={() => setIsConfirmOpen(false)}
            onConfirmClick={handleConfirm}
            title={confirmTitle}
            body={confirmBody}
            confirmButtonText={confirmButtonText}
            cancelButtonText={cancelButtonText}
          />
        )}
      </>
    )
  }
)

export default RMIconButton
