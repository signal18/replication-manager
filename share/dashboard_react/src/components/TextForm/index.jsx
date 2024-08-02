import { Box, Flex, Input, Spinner } from '@chakra-ui/react'
import React, { useEffect, useState, useRef } from 'react'
import { HiCheck, HiPencilAlt, HiX } from 'react-icons/hi'
import styles from './styles.module.scss'
import RMIconButton from '../RMIconButton'

function TextForm({ onConfirm, id, label, originalValue, loading, maxLength = 120, className, direction }) {
  const [value, setValue] = useState('')
  const [isEditable, setIsEditable] = useState(false)
  const inputRef = useRef(null)

  useEffect(() => {
    if (originalValue) {
      setValue(originalValue)
    }
  }, [originalValue])

  const handleChange = (e) => {
    setValue(e.target.value)
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
          value={value}
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
                setValue(originalValue)
              }}
            />
            <RMIconButton
              icon={HiCheck}
              colorScheme='green'
              tooltip='Save'
              onClick={() => {
                onConfirm(value)
                setIsEditable(false)
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
    </Flex>
  )
}

export default TextForm
