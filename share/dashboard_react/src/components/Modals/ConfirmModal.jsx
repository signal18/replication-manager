import { Modal, ModalCloseButton, ModalContent, ModalFooter, ModalHeader, ModalOverlay } from '@chakra-ui/react'
import React from 'react'
import Button from '../Button'

function ConfirmModal({ title, isOpen, closeModal, onConfirmClick }) {
  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent>
        <ModalHeader whiteSpace='pre-line'>{title}</ModalHeader>
        <ModalCloseButton />

        <ModalFooter gap={3}>
          <Button variant='outline' size='medium' onClick={closeModal}>
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
