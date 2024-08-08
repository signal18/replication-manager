import { Flex, HStack, Radio, RadioGroup, VStack } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import Dropdown from '../../../components/Dropdown'
import styles from './styles.module.scss'
import TimePicker from 'react-time-picker'
import { getDaysInMonth, padWithZero } from '../../../utility/common'
import RMButton from '../../../components/RMButton'
import ConfirmModal from '../../../components/Modals/ConfirmModal'
import RMSwitch from '../../../components/RMSwitch'
import RMIconButton from '../../../components/RMIconButton'
import { GrPowerReset } from 'react-icons/gr'
import Message from '../../../components/Message'
import RMTextarea from '../../../components/RMTextarea'

function RegexText({
  value,
  user,
  isSwitchChecked,
  onSave,
  hasSwitch = true,
  onSwitchChange,
  confirmTitle,
  switchConfirmTitle
}) {
  const [currentValue, setCurrentValue] = useState(value)
  const [previousValue, setPreviousValue] = useState(value)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [errorMessage, setErrorMessage] = useState('')
  const [valuesChanged, setValuesChanged] = useState(false)

  useEffect(() => {
    setCurrentValue(value)
    setPreviousValue(value)
  }, [value])

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }
  const closeConfirmModal = (action) => {
    if (action === 'cancel') {
      setCurrentValue(previousValue)
    }
    setIsConfirmModalOpen(false)
  }

  const handleRegexChange = (e) => {
    setValuesChanged(true)
    setCurrentValue(e.target.value)
  }

  const handleSaveRegex = () => {
    openConfirmModal()
  }

  return (
    <VStack className={styles.scheduler} align='flex-start'>
      {hasSwitch && (
        <RMSwitch
          confirmTitle={switchConfirmTitle}
          onChange={onSwitchChange}
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={isSwitchChecked}
        />
      )}

      {(!hasSwitch || isSwitchChecked) && (
        <>
          <RMTextarea value={currentValue} handleInputChange={handleRegexChange} />
          {valuesChanged && (
            <HStack>
              <RMButton isDisabled={errorMessage?.length > 0} onClick={handleSaveRegex}>
                Save
              </RMButton>
              <RMIconButton
                icon={GrPowerReset}
                tooltip={'Reset regex'}
                onClick={() => {
                  setValuesChanged(false)
                  setCurrentValue(previousValue)
                  setErrorMessage('')
                }}
              />
            </HStack>
          )}
        </>
      )}

      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={() => {
            closeConfirmModal('cancel')
          }}
          title={`${confirmTitle}`}
          onConfirmClick={() => {
            onSave(currentValue)
            closeConfirmModal('')
          }}
        />
      )}
    </VStack>
  )
}

export default RegexText
