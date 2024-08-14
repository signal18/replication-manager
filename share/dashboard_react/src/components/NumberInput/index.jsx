import { HStack, Input, useNumberInput } from '@chakra-ui/react'
import React, { useRef } from 'react'
import RMIconButton from '../RMIconButton'
import { HiOutlineMinusCircle, HiOutlinePlusCircle } from 'react-icons/hi'
import styles from './styles.module.scss'

function NumberInput({ min = 2, max = 120, step = 1, defaultValue, value, onChange, readonly = false }) {
  const inputRef = useRef(null)
  console.log('readonly::', readonly)
  const { getInputProps, getIncrementButtonProps, getDecrementButtonProps } = useNumberInput({
    step: step,
    defaultValue: defaultValue,
    value: value,
    min: min,
    max: max,
    onChange: (valueAsString, valueAsNumber) => onChange(valueAsString, valueAsNumber)
  })
  const inc = getIncrementButtonProps()
  const dec = getDecrementButtonProps()
  const input = getInputProps()

  return (
    <HStack spacing='3' className={readonly ? styles.readonly : ''}>
      <RMIconButton {...dec} icon={HiOutlineMinusCircle} aria-label='Decrement' />
      <Input {...input} width='75px' size='sm' ref={inputRef} readOnly={readonly} />
      <RMIconButton {...inc} icon={HiOutlinePlusCircle} aria-label='Increment' />
    </HStack>
  )
}

export default NumberInput
