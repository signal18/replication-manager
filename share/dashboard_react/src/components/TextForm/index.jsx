import { Flex, Input, Spinner } from '@chakra-ui/react'
import React, { useEffect, useState, useRef } from 'react'
import { HiCheck, HiPencilAlt, HiX } from 'react-icons/hi'
import styles from './styles.module.scss'
import IconButton from '../IconButton'

function TextForm({ onConfirm, originalValue, loading, maxLength = 120 }) {
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
    <Flex className={styles.textContainer}>
      <Input ref={inputRef} value={value} maxLength={maxLength} readOnly={!isEditable} onChange={handleChange} />
      {isEditable ? (
        <>
          <IconButton
            icon={HiX}
            tooltip='Cancel'
            colorScheme='red'
            onClick={() => {
              setIsEditable(false)
              setValue(originalValue)
            }}
          />
          <IconButton
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
        <IconButton
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
  )
}

export default TextForm
