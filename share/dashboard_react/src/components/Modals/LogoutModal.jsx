import { Button, Modal, ModalCloseButton, ModalContent, ModalFooter, ModalHeader, ModalOverlay } from '@chakra-ui/react'
import React from 'react'

function LogoutModal(props) {
  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent>
        <ModalHeader whiteSpace='pre-line'>{title}</ModalHeader>
        <ModalCloseButton />

        <ModalFooter>
          <Button variant='outline' mr={3} onClick={closeModal}>
            Cancel
          </Button>
          <Button colorScheme='blue' onClick={onConfirmClick}>
            Confirm
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default LogoutModal
