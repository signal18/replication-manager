import React from 'react'
import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton,
  Code, Box, Text
} from '@chakra-ui/react'

function TagContentModal({ isOpen, closeModal, tagName, content }) {
  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='xl'>
      <ModalOverlay />
      <ModalContent>
        <ModalHeader>Tag: {tagName}</ModalHeader>
        <ModalCloseButton />
        <ModalBody pb={6}>
          {content ? (
            <Box
              as='pre'
              p={4}
              bg='gray.800'
              color='green.200'
              borderRadius='md'
              fontSize='sm'
              overflowX='auto'
              whiteSpace='pre-wrap'
              wordBreak='break-all'
              maxH='60vh'
              overflowY='auto'
            >
              <Code bg='transparent' color='inherit' whiteSpace='pre-wrap'>
                {content}
              </Code>
            </Box>
          ) : (
            <Text color='gray.400' fontSize='sm'>No content found for this tag.</Text>
          )}
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

export default TagContentModal
