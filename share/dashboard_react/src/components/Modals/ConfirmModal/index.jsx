import {
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay
} from '@chakra-ui/react'
import { useCallback, useEffect, useRef } from 'react'
import RMButton from '../../RMButton'
import styles from './styles.module.scss'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'
import { acquireAutoReloadPause, releaseAutoReloadPause } from '../../../utility/autoReloadPause'

function ConfirmModal({
  title,
  isOpen,
  closeModal,
  body,
  onConfirmClick,
  showCancelButton = true,
  showConfirmButton = true,
  closeOnOverlayClick = true,
  closeOnEsc = true,
  cancelButtonText = 'Cancel',
  confirmButtonText = 'Confirm',
  confirmButtonProps = {}
}) {
  const { theme } = useTheme()
  const pauseRegisteredRef = useRef(false)

  const pauseAutoReloadForConfirmModal = useCallback(() => {
    if (pauseRegisteredRef.current) return
    acquireAutoReloadPause()
    pauseRegisteredRef.current = true
  }, [])

  const resumeAutoReloadFromConfirmModal = useCallback(() => {
    if (!pauseRegisteredRef.current) return
    releaseAutoReloadPause()
    pauseRegisteredRef.current = false
  }, [])

  useEffect(() => {
    if (isOpen) {
      pauseAutoReloadForConfirmModal()
    } else {
      resumeAutoReloadFromConfirmModal()
    }

    return () => {
      resumeAutoReloadFromConfirmModal()
    }
  }, [isOpen, pauseAutoReloadForConfirmModal, resumeAutoReloadFromConfirmModal])

  return (
    <Modal isOpen={isOpen} onClose={closeModal} closeOnOverlayClick={closeOnOverlayClick} closeOnEsc={closeOnEsc}>
      <ModalOverlay />
      <ModalContent
        className={`${styles.modalContent} ${theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}`}>
        {title && <ModalHeader className={styles.modalHeader}>{title}</ModalHeader>}
        {title && <ModalCloseButton /> }
        <ModalBody className={styles.modalBody}>{body}</ModalBody>
        {!title && <ModalCloseButton /> }
        <ModalFooter gap={3}>
          {showCancelButton && (
            <RMButton variant='outline' colorScheme='white' size='medium' onClick={closeModal}>
              {cancelButtonText}
            </RMButton>
          )}
          {showConfirmButton && (
            <RMButton colorScheme='blue' size='medium' onClick={onConfirmClick} {...confirmButtonProps}>
              {confirmButtonText}
            </RMButton>
          )}
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default ConfirmModal
