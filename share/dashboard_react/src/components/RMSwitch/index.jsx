import { Flex, Spinner, Switch, Text } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import styles from './styles.module.scss'
import ConfirmModal from '../Modals/ConfirmModal'

function RMSwitch({
  id,
  onText = 'ON',
  offText = 'OFF',
  isChecked,
  size = 'md',
  isDisabled,
  onConfirm,
  confirmTitle,
  loading
}) {
  const [currentValue, setCurrentValue] = useState(isChecked)
  const [previousValue, setPreviousValue] = useState(isChecked)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)

  useEffect(() => {
    setCurrentValue(isChecked)
    setPreviousValue(isChecked)
  }, [isChecked])

  const handleChange = (e) => {
    setCurrentValue(e.target.checked)
    openConfirmModal()
  }

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }
  const closeConfirmModal = (action) => {
    if (action === 'cancel') {
      setCurrentValue(previousValue)
    }
    setIsConfirmModalOpen(false)
  }

  return (
    <Flex className={styles.switchContainer} align='center'>
      <Switch size={size} id={id} isChecked={currentValue} isDisabled={isDisabled} onChange={handleChange} />
      <Text className={`${styles.text} ${currentValue ? styles.green : styles.red}`}>
        {currentValue ? onText : offText}
      </Text>
      {loading && <Spinner />}
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={() => {
            closeConfirmModal('cancel')
          }}
          title={confirmTitle}
          onConfirmClick={() => {
            onConfirm(currentValue)
            closeConfirmModal('')
          }}
        />
      )}
    </Flex>
  )
}

export default RMSwitch
