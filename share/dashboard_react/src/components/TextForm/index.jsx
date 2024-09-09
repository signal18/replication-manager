import { Box, Flex, Input, Spinner } from '@chakra-ui/react'
import React, { useEffect, useState, useRef } from 'react'
import { HiCheck, HiPencilAlt, HiX } from 'react-icons/hi'
import styles from './styles.module.scss'
import RMIconButton from '../RMIconButton'
import ConfirmModal from '../Modals/ConfirmModal'

function TextForm({ onSave, id, label, value, loading, maxLength = 120, className, direction, confirmTitle }) {
  const [isEditable, setIsEditable] = useState(false)
  const inputRef = useRef(null)

  const [currentValue, setCurrentValue] = useState('')
  const [previousValue, setPreviousValue] = useState('')
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)

  useEffect(() => {
    if (value) {
      setCurrentValue(value)
      setPreviousValue(value)
    }
  }, [value])

  const handleChange = (e) => {
    setCurrentValue(e.target.value)
  }

  return (
    <Flex className={`${styles.textContainer} ${className}`} direction={direction}>
      {label && (
        <label className={styles.label} htmlFor={id}>
          {label}
        </label>
      )}
      <Flex w='100%' gap='2' align='center'>
        <Input
          id={id}
          ref={inputRef}
          value={currentValue}
          maxLength={maxLength}
          readOnly={!isEditable}
          onChange={handleChange}
        />
        {isEditable ? (
          <>
            <RMIconButton
              icon={HiX}
              tooltip='Cancel'
              colorScheme='red'
              onClick={() => {
                setIsEditable(false)
                setCurrentValue(previousValue)
              }}
            />
            <RMIconButton
              icon={HiCheck}
              colorScheme='green'
              tooltip='Save'
              onClick={() => {
                setIsConfirmModalOpen(true)
              }}
            />
          </>
        ) : (
          <RMIconButton
            icon={HiPencilAlt}
            tooltip='Edit'
            onClick={() => {
              setIsEditable(true)
              inputRef.current.focus()
            }}
          />
        )}
        {loading && <Spinner />}
      </Flex>
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={() => setIsConfirmModalOpen(false)}
          title={`${confirmTitle} ${currentValue}`}
          onConfirmClick={() => {
            onSave(currentValue)
            setIsConfirmModalOpen(false)
          }}
        />
      )}
    </Flex>
  )
}

export default TextForm
