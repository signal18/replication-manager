import {
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay
} from '@chakra-ui/react'
import React from 'react'
import RMButton from '../../RMButton'
import styles from './styles.module.scss'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'

function ConfirmModal({
  title,
  isOpen,
  closeModal,
  body,
  onConfirmClick,
  showCancelButton = true,
  showConfirmButton = true,
  cancelButtonText = 'Cancel',
  confirmButtonText = 'Confirm'
}) {
  const { theme } = useTheme()

  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent
        className={`${styles.modalContent} ${theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}`}>
        {title && <ModalHeader className={styles.modalHeader}>{title}</ModalHeader>}

        <ModalBody className={styles.modalBody}>{body}</ModalBody>
        <ModalCloseButton />

        <ModalFooter gap={3}>
          {showCancelButton && (
            <RMButton variant='outline' colorScheme='white' size='medium' onClick={closeModal}>
              {cancelButtonText}
            </RMButton>
          )}
          {showConfirmButton && (
            <RMButton colorScheme='blue' size='medium' onClick={onConfirmClick}>
              {confirmButtonText}
            </RMButton>
          )}
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default ConfirmModal
