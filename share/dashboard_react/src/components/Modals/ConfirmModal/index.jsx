import { Modal, ModalCloseButton, ModalContent, ModalFooter, ModalHeader, ModalOverlay } from '@chakra-ui/react'
import React from 'react'
import RMButton from '../../RMButton'
import styles from './styles.module.scss'
import { useTheme } from '../../../ThemeProvider'

function ConfirmModal({ title, isOpen, closeModal, onConfirmClick }) {
  const { theme } = useTheme()

  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent
        className={`${styles.modalContent} ${theme === 'light' ? styles.modalLightContent : styles.modalDarkContent}`}>
        <ModalHeader className={styles.modalHeader}>{title}</ModalHeader>
        <ModalCloseButton />

        <ModalFooter gap={3}>
          <RMButton variant='outline' colorScheme='white' size='medium' onClick={closeModal}>
            Cancel
          </RMButton>
          <RMButton colorScheme='blue' size='medium' onClick={onConfirmClick}>
            Confirm
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default ConfirmModal
