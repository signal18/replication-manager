import {
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay
} from '@chakra-ui/react'
import React, { useEffect, useRef } from 'react'
import RMButton from '../../RMButton'
import styles from './styles.module.scss'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'
import { AUTO_RELOAD_PAUSE_KEY } from '../../../utility/autoReloadPause'

const CONFIRM_MODAL_PAUSE_TOKEN = 'confirm-modal'
const CONFIRM_MODAL_PAUSE_COUNT_KEY = 'pause_auto_reload_confirm_modal_count'

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

  const getConfirmModalPauseCount = () => {
    const pauseCount = parseInt(localStorage.getItem(CONFIRM_MODAL_PAUSE_COUNT_KEY) || '0', 10)
    return Number.isNaN(pauseCount) ? 0 : pauseCount
  }

  const pauseAutoReloadForConfirmModal = () => {
    if (pauseRegisteredRef.current) {
      return
    }

    const currentPauseState = localStorage.getItem(AUTO_RELOAD_PAUSE_KEY)
    const currentPauseCount = getConfirmModalPauseCount()

    localStorage.setItem(CONFIRM_MODAL_PAUSE_COUNT_KEY, String(currentPauseCount + 1))
    pauseRegisteredRef.current = true

    if (!currentPauseState) {
      localStorage.setItem(AUTO_RELOAD_PAUSE_KEY, CONFIRM_MODAL_PAUSE_TOKEN)
    }
  }

  const resumeAutoReloadFromConfirmModal = () => {
    if (!pauseRegisteredRef.current) {
      return
    }

    const currentPauseCount = getConfirmModalPauseCount()
    const nextPauseCount = Math.max(0, currentPauseCount - 1)

    if (nextPauseCount === 0) {
      localStorage.removeItem(CONFIRM_MODAL_PAUSE_COUNT_KEY)

      if (localStorage.getItem(AUTO_RELOAD_PAUSE_KEY) === CONFIRM_MODAL_PAUSE_TOKEN) {
        localStorage.removeItem(AUTO_RELOAD_PAUSE_KEY)
      }
    } else {
      localStorage.setItem(CONFIRM_MODAL_PAUSE_COUNT_KEY, String(nextPauseCount))
    }

    pauseRegisteredRef.current = false
  }

  useEffect(() => {
    if (isOpen) {
      pauseAutoReloadForConfirmModal()
    } else {
      resumeAutoReloadFromConfirmModal()
    }

    return () => {
      resumeAutoReloadFromConfirmModal()
    }
  }, [isOpen])

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
