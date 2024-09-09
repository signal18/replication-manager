import { Box, HStack, Spinner } from '@chakra-ui/react'
import React from 'react'
import { useSelector } from 'react-redux'
import MenuOptions from '../MenuOptions'
import RMButton from '../RMButton'
import styles from './styles.module.scss'

function Card({
  header,
  body,
  headerAction,
  menuOptions,
  buttonText,
  buttonColorScheme,
  isButtonBlinking = false,
  isLoading,
  loadingText,
  onClick,
  width,
  className
}) {
  const {
    common: { isDesktop }
  } = useSelector((state) => state)

  return (
    <Box className={`${styles.card} ${className}`} w={width}>
      <HStack size={'sm'} className={styles.heading}>
        {headerAction === 'menu' && (
          <MenuOptions placement='right' options={menuOptions} subMenuPlacement={isDesktop ? 'right' : 'bottom'} />
        )}
        {headerAction === 'button' && (
          <RMButton
            isBlinking={isButtonBlinking}
            colorScheme={buttonColorScheme}
            onClick={onClick}
            isLoading={isLoading}
            loadingText={loadingText}>
            {buttonText}
          </RMButton>
        )}
        {headerAction !== 'button' && isLoading && <Spinner label={loadingText} speed='1s' />}
        {header}
      </HStack>
      {body}
    </Box>
  )
}

export default Card
