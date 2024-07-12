import { Button, Modal, ModalCloseButton, ModalContent, ModalFooter, ModalHeader, ModalOverlay } from '@chakra-ui/react'
import React from 'react'

function ConfirmModal({ title, isOpen, closeModal, onConfirmClick }) {
  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent>
        <ModalHeader>{title}</ModalHeader>
        <ModalCloseButton />

        <ModalFooter>
          <Button colorScheme='blue' mr={3} onClick={closeModal}>
            No
          </Button>
          <Button variant='ghost' onClick={onConfirmClick}>
            Yes
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default ConfirmModal
