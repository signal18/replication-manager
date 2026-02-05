import { HStack, Input, useNumberInput } from '@chakra-ui/react'
import React, { useRef, useState, useEffect } from 'react'
import RMIconButton from '../RMIconButton'
import {
  HiCheck,
  HiChevronDoubleDown,
  HiChevronDoubleUp,
  HiOutlineMinusCircle,
  HiOutlinePlusCircle,
  HiPencilAlt,
  HiX
} from 'react-icons/hi'
import styles from './styles.module.scss'
import ConfirmModal from '../Modals/ConfirmModal'

function NumberInput({
  min = 2,
  max = 120,
  step = 1,
  secondaryStep = 0,
  inputWidth ='75px',
  defaultValue,
  value,
  isDisabled,
  onChange,
  showEditButton = false,
  showConfirmModal = false,
  confirmTitle = 'Confirm change',
  confirmBody = 'Are you sure you want to change the value to: ',
  onConfirm,
  onConfirmValidator = (value) => true,
  containerClassName
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

  const clampValue = (nextValue) => Math.min(max, Math.max(min, nextValue))

  const applyStepChange = (stepValue) => {
    const baseValue = Number.isFinite(currentValue) ? currentValue : 0
    const nextValue = clampValue(baseValue + stepValue)

    if (onChange) {
      onChange(String(nextValue), nextValue)
      return
    }

    setCurrentValue(nextValue)
  }

  const handleChange = (valueAsString, valueAsNumber) => {
    if (valueAsString) {
      setCurrentValue(valueAsNumber)
    } else {
      setCurrentValue(0)
    }
  }

  return (
    <>
      <HStack className={`${styles.container} ${containerClassName}`}>
        <HStack spacing='3' className={isReadOnly ? styles.readonly : ''}>
          {secondaryStep > 0 ? (
            <RMIconButton
              icon={HiChevronDoubleDown}
              aria-label='Secondary decrement'
              tooltip={`-${secondaryStep}`}
              onClick={() => applyStepChange(-secondaryStep)}
            />
          ) : null}
          <RMIconButton {...dec} icon={HiOutlineMinusCircle} aria-label='Decrement' tooltip={`-${step}`} />
          <Input {...input} width={inputWidth} size='sm' ref={inputRef} readOnly={isReadOnly} />
          <RMIconButton {...inc} icon={HiOutlinePlusCircle} aria-label='Increment' tooltip={`+${step}`} />
          {secondaryStep > 0 ? (
            <RMIconButton
              icon={HiChevronDoubleUp}
              aria-label='Secondary increment'
              tooltip={`+${secondaryStep}`}
              onClick={() => applyStepChange(secondaryStep)}
            />
          ) : null}
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
                  if (!onConfirmValidator(currentValue)) {
                    return
                  }

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
          title={`${confirmTitle}`}
          body={`${confirmBody} "${currentValue}"?`}
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
