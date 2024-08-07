import { Modal, ModalCloseButton, ModalContent, ModalFooter, ModalHeader, ModalOverlay } from '@chakra-ui/react'
import React from 'react'
import Button from '../../Button'
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
          <Button variant='outline' colorScheme='white' size='medium' onClick={closeModal}>
            Cancel
          </Button>
          <Button colorScheme='blue' size='medium' onClick={onConfirmClick}>
            Confirm
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default ConfirmModal
