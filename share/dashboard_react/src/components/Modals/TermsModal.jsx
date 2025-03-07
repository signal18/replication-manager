import {
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
} from '@chakra-ui/react'
import React, { useState } from 'react'
import Markdown from 'react-markdown'
import RMButton from '../RMButton'
import { useTheme } from '../../ThemeProvider'
import parentStyles from './styles.module.scss'
import remarkGFM from 'remark-gfm'

function TermsModal({ title = 'Terms and Conditions', submitText = 'I agree with all terms and condition mentioned above', isOpen, closeModal, onAgreeTerms, above = <></>, terms = ``, below = <></>, cluster }) {
  const { theme } = useTheme()
  const [agree, setAgree] = useState(false)

  const handleSubmit = () => {
    if (onAgreeTerms) {
      onAgreeTerms(cluster)
    }
  }

  return (
    <Modal size={'xl'} isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{title}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='5'>
            {above}
            <Markdown  remarkPlugins={[remarkGFM]}>{terms}</Markdown>
            <FormControl>
              <Checkbox isChecked={agree} onChange={(e) => setAgree(!!e.target.checked)}>{submitText}</Checkbox>
            </FormControl>
            {below}
          </Stack>
        </ModalBody>

        <ModalFooter gap={3} margin='auto'>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={closeModal}>
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
