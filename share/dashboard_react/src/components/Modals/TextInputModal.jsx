import {
  FormControl,
  FormErrorMessage,
  FormLabel,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Stack
} from '@chakra-ui/react'
import React, { useState } from 'react'
import RMButton from '../RMButton'
import { useTheme } from '../../ThemeProvider'
import parentStyles from './styles.module.scss'

function TextInputModal({ isOpen, closeModal, title, fieldname, defaultValue = '', regexPattern = '', isRequired = true, onSave }) {
  const { theme } = useTheme()
  const [value, setValue] = useState(defaultValue)
  const [valueError, setValueError] = useState('')

  const handleSave = () => {
    setValueError('')

    if (value !== "" && regexPattern && !new RegExp(regexPattern).test(value)) {
      setValueError(`Invalid pattern value for ${fieldname}`)
      return
    }

    if (isRequired && !value) {
      setValueError(`${fieldname} cannot be empty`)
      return
    }

    if (onSave) {
      onSave(value)
    }

    setValue('')
    closeModal()
  }

  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{title}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='5'>
            <FormControl isInvalid={valueError}>
              <FormLabel htmlFor={fieldname}>{fieldname}</FormLabel>
              <Input
                id={fieldname}
                type='text'
                isRequired={true}
                value={value}
                onChange={(e) => setValue(e.target.value)}
              />
              <FormErrorMessage>{valueError}</FormErrorMessage>
            </FormControl>
          </Stack>
        </ModalBody>

        <ModalFooter gap={3} margin='auto'>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={closeModal}>
            Cancel
          </RMButton>
          <RMButton onClick={handleSave} size='medium'>
            Save
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default TextInputModal
