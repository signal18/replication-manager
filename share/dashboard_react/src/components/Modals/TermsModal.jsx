import {
  Box,
  Checkbox,
  FormControl,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Stack,
  Text,
} from '@chakra-ui/react'
import React, { useState, useRef } from 'react'
import Markdown from 'react-markdown'
import RMButton from '../RMButton'
import { useTheme } from '../../ThemeProvider'
import parentStyles from './styles.module.scss'
import remarkGFM from 'remark-gfm'

function TermsModal({
  title = 'Terms and Conditions',
  submitText = 'I agree with all terms and condition mentioned above',
  isOpen,
  closeModal,
  onAgreeTerms,
  above = <></>,
  terms = ``,
  below = <></>,
  cluster
}) {
  const { theme } = useTheme()
  const [agree, setAgree] = useState(false)
  const [hasScrolledToBottom, setHasScrolledToBottom] = useState(false)
  const scrollContainerRef = useRef(null)

  // Theme-based styling to match your custom theme system
  const getScrollStyles = () => {
    if (theme === 'dark') {
      return {
        bg: '#2D3748', // Dark gray background for dark theme
        borderColor: 'var(--secondary-color)',
        color: 'white'
      }
    }
    return {
      bg: 'var(--white-color)', // White background for light theme (as it was working)
      borderColor: 'var(--secondary-color)',
      color: 'black'
    }
  }

  const scrollStyles = getScrollStyles()
  const hintColor = theme === 'dark' ? 'red.300' : 'red.500'

  const handleSubmit = () => {
    if (onAgreeTerms) {
      onAgreeTerms(cluster)
    }
  }

  const handleScroll = (e) => {
    const { scrollTop, scrollHeight, clientHeight } = e.target
    const isAtBottom = scrollTop + clientHeight >= scrollHeight - 10 // 10px tolerance
    if (isAtBottom && !hasScrolledToBottom) {
      setHasScrolledToBottom(true)
    }
  }

  const handleModalClose = () => {
    // Reset states when modal closes
    setAgree(false)
    setHasScrolledToBottom(false)
    closeModal()
  }

  const handleCheckboxChange = (e) => {
    setAgree(!!e.target.checked)
  }

  return (
    <Modal size={'xl'} isOpen={isOpen} onClose={handleModalClose}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{title}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='5'>
            {above}

            {/* Scrollable Terms Container */}
            <Box
              ref={scrollContainerRef}
              maxHeight="400px"
              overflowY="auto"
              border="1px solid"
              borderColor={scrollStyles.borderColor}
              borderRadius="md"
              bg={scrollStyles.bg}
              color={scrollStyles.color}
              p={4}
              onScroll={handleScroll}
              css={{
                '&::-webkit-scrollbar': {
                  width: '8px',
                },
                '&::-webkit-scrollbar-track': {
                  background: 'var(--chakra-colors-gray-100)',
                },
                '&::-webkit-scrollbar-thumb': {
                  background: 'var(--chakra-colors-gray-400)',
                  borderRadius: '4px',
                },
                '&::-webkit-scrollbar-thumb:hover': {
                  background: 'var(--chakra-colors-gray-500)',
                },
              }}
            >
              <Markdown remarkPlugins={[remarkGFM]}>{terms}</Markdown>
            </Box>

            <FormControl>
              <Stack spacing={2}>
                <Checkbox
                  isChecked={agree}
                  onChange={handleCheckboxChange}
                  isDisabled={!hasScrolledToBottom}
                  size="md"
                >
                  {submitText}
                </Checkbox>
                {!hasScrolledToBottom && (
                  <Text fontSize="sm" color={hintColor} fontStyle="italic" ml={6}>
                    Please scroll through the entire terms and conditions above to enable this option
                  </Text>
                )}
              </Stack>
            </FormControl>

            {below}
          </Stack>
        </ModalBody>
        <ModalFooter gap={3} margin='auto'>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={handleModalClose}>
            Cancel
          </RMButton>
          <RMButton onClick={handleSubmit} isDisabled={!agree} size='medium'>
            Submit
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default TermsModal
