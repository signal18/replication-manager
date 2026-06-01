import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton, ModalFooter,
  FormControl, FormLabel, Input, Textarea, Select, HStack
} from '@chakra-ui/react'
import React, { useState } from 'react'
import RMButton from '../../RMButton'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'

function InterventionModal({ isOpen, closeModal, onStart }) {
  const { theme } = useTheme()
  const [user, setUser] = useState('')
  const [description, setDescription] = useState('')
  const [estimatedTime, setEstimatedTime] = useState('30')
  const [unit, setUnit] = useState('minutes')

  const handleStart = () => {
    const est = `${estimatedTime} ${unit}`
    const reason = description ? `${description} (est. ${est})` : `Intervention (est. ${est})`
    onStart({ user, reason, estimatedTime: est })
    setUser('')
    setDescription('')
    setEstimatedTime('30')
    setUnit('minutes')
  }

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='lg'>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader fontSize='md'>Start Intervention — all notifications will be silenced</ModalHeader>
        <ModalCloseButton />
        <ModalBody pb={4}>
          <FormControl mb={3}>
            <FormLabel fontSize='sm'>Operator name</FormLabel>
            <Input
              size='sm'
              placeholder='Who is performing the intervention'
              value={user}
              onChange={(e) => setUser(e.target.value)}
            />
          </FormControl>
          <FormControl mb={3} isRequired>
            <FormLabel fontSize='sm'>Description</FormLabel>
            <Textarea
              size='sm'
              placeholder='What are you doing? (e.g. rolling restart for v3.1.25, config change, DB upgrade)'
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={3}
            />
          </FormControl>
          <FormControl>
            <FormLabel fontSize='sm'>Estimated duration</FormLabel>
            <HStack>
              <Input
                size='sm'
                type='number'
                width='100px'
                value={estimatedTime}
                onChange={(e) => setEstimatedTime(e.target.value)}
              />
              <Select size='sm' width='130px' value={unit} onChange={(e) => setUnit(e.target.value)}>
                <option value='minutes'>minutes</option>
                <option value='hours'>hours</option>
              </Select>
            </HStack>
          </FormControl>
        </ModalBody>
        <ModalFooter>
          <RMButton onClick={closeModal} mr={3}>Cancel</RMButton>
          <RMButton
            colorScheme='orange'
            onClick={handleStart}
            isDisabled={!description}
          >
            Start Intervention
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default InterventionModal
