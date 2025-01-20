import { HStack, Input, useNumberInput } from '@chakra-ui/react'
import React, { useRef, useState, useEffect } from 'react'
import RMIconButton from '../RMIconButton'
import { HiCheck, HiOutlineMinusCircle, HiOutlinePlusCircle, HiPencilAlt, HiX } from 'react-icons/hi'
import styles from './styles.module.scss'
import ConfirmModal from '../Modals/ConfirmModal'

function NumberInput({
  min = 2,
  max = 120,
  step = 1,
  defaultValue,
  value,
  isDisabled,
  onChange,
  showEditButton = false,
  showConfirmModal = false,
  confirmTitle = 'Confirm change to:',
  onConfirm
}) {
  const inputRef = useRef(null)

  const [isReadOnly, setIsReadOnly] = useState(showEditButton ? true : false)
  const [currentValue, setCurrentValue] = useState(0)
  const [previousValue, setPreviousValue] = useState(0)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)

  useEffect(() => {
    if (value) {
      setCurrentValue(value)
      setPreviousValue(value)
    }
  }, [value])

  const { getInputProps, getIncrementButtonProps, getDecrementButtonProps } = useNumberInput({
    step: step,
    defaultValue: defaultValue,
    value: currentValue,
    min: min,
    max: max,
    onChange: (valueAsString, valueAsNumber) =>
      onChange ? onChange(valueAsString, valueAsNumber) : handleChange(valueAsString, valueAsNumber)
  })
  const inc = getIncrementButtonProps()
  const dec = getDecrementButtonProps()
  const input = getInputProps()

  const handleChange = (valueAsString, valueAsNumber) => {
    if (valueAsString) {
      setCurrentValue(valueAsNumber)
    } else {
      setCurrentValue(0)
    }
  }

  return (
    <>
      <HStack>
        <HStack spacing='3' className={isReadOnly ? styles.readonly : ''}>
          <RMIconButton {...dec} icon={HiOutlineMinusCircle} aria-label='Decrement' />
          <Input {...input} width='75px' size='sm' ref={inputRef} readOnly={isReadOnly} />
          <RMIconButton {...inc} icon={HiOutlinePlusCircle} aria-label='Increment' />
        </HStack>
        {showEditButton && !isDisabled ? (
          isReadOnly ? (
            <RMIconButton
              icon={HiPencilAlt}
              tooltip='Edit'
              onClick={() => {
                setIsReadOnly(!isReadOnly)
              }}
            />
          ) : (
            <>
              <RMIconButton
                icon={HiX}
                tooltip='Cancel'
                colorScheme='red'
                onClick={() => {
                  setIsReadOnly(true)
                  setCurrentValue(previousValue)
                }}
              />
              <RMIconButton
                icon={HiCheck}
                colorScheme='green'
                tooltip='Save'
                onClick={() => {
                  if (showConfirmModal) {
                    setIsConfirmModalOpen(true)
                  } else {
                    setIsReadOnly(true)
                    onConfirm(currentValue)
                  }
                }}
              />
            </>
          )
        ) : null}
      </HStack>
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={() => setIsConfirmModalOpen(false)}
          title={`${confirmTitle} ${currentValue}`}
          onConfirmClick={() => {
            onConfirm(currentValue)
            setIsReadOnly(true)
            setIsConfirmModalOpen(false)
          }}
        />
      )}
    </>
  )
}

export default NumberInput
