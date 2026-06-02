import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton, ModalFooter,
  FormControl, FormLabel, Textarea, Select, HStack, Input, Checkbox
} from '@chakra-ui/react'
import React, { useState } from 'react'
import RMButton from '../../RMButton'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'

function InterventionModal({ isOpen, closeModal, onStart }) {
  const { theme } = useTheme()
  const [description, setDescription] = useState('')
  const [estimatedTime, setEstimatedTime] = useState('30')
  const [unit, setUnit] = useState('minutes')
  const [autoUnmute, setAutoUnmute] = useState(true)

  // Default start time: now, formatted for datetime-local input
  const nowLocal = () => {
    const d = new Date()
    d.setMinutes(d.getMinutes() - d.getTimezoneOffset())
    return d.toISOString().slice(0, 16)
  }
  const [startTime, setStartTime] = useState(nowLocal())

  const handleStart = () => {
    const est = `${estimatedTime} ${unit}`
    const reason = description ? `${description} (est. ${est})` : `Intervention (est. ${est})`
    const startAt = new Date(startTime).toISOString()
    let endAt = ''
    if (autoUnmute) {
      const start = new Date(startTime)
      const durationMs = unit === 'hours'
        ? parseInt(estimatedTime) * 60 * 60 * 1000
        : parseInt(estimatedTime) * 60 * 1000
      endAt = new Date(start.getTime() + durationMs).toISOString()
    }
    onStart({ reason, startAt, endAt, autoUnmute })
    setDescription('')
    setEstimatedTime('30')
    setUnit('minutes')
    setAutoUnmute(true)
    setStartTime(nowLocal())
  }

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='lg'>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader fontSize='md'>Start Intervention — all notifications will be silenced</ModalHeader>
        <ModalCloseButton />
        <ModalBody pb={4}>
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
          <FormControl mb={3}>
            <FormLabel fontSize='sm'>Start time</FormLabel>
            <Input
              size='sm'
              type='datetime-local'
              width='250px'
              value={startTime}
              onChange={(e) => setStartTime(e.target.value)}
            />
          </FormControl>
          <FormControl mb={3}>
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
          <FormControl>
            <Checkbox
              isChecked={autoUnmute}
              onChange={(e) => setAutoUnmute(e.target.checked)}
              size='sm'
            >
              Auto-unmute after estimated duration
            </Checkbox>
          </FormControl>
        </ModalBody>
        <ModalFooter>
          <RMButton onClick={closeModal} mr={3}>Cancel</RMButton>
          <RMButton
            colorScheme='orange'
            onClick={handleStart}
            isDisabled={!description}
          >
            {new Date(startTime) > new Date() ? 'Schedule Intervention' : 'Start Intervention'}
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default InterventionModal
