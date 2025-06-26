import { Box, Flex, Input, Spinner, useDisclosure } from '@chakra-ui/react'
import React, { useEffect, useState, useRef, useMemo } from 'react'
import { HiCheck, HiEye, HiEyeOff, HiFolder, HiPencilAlt, HiX } from 'react-icons/hi'
import styles from './styles.module.scss'
import RMIconButton from '../RMIconButton'
import ConfirmModal from '../Modals/ConfirmModal'
import TreeView from '../Modals/TreeView/TreeView'

function TextForm({ onSave, id, type, label, value, loading, maxLength = 120, className, direction, confirmTitle,
  confirmBody = "Are you sure you want to change the value to: ",
  regexPattern, isDisabled, isTree = false, treeData, nodeToValue, nodeToString, ...others }) {
  const [isEditable, setIsEditable] = useState(false)
  const inputRef = useRef(null)
  const { isOpen, onToggle } = useDisclosure()

  const onClickReveal = () => {
    onToggle()
    if (inputRef.current) {
      inputRef.current.focus({ preventScroll: true })
    }
  }

  const [currentValue, setCurrentValue] = useState('')
  const [previousValue, setPreviousValue] = useState('')
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [isTreeModalOpen, setIsTreeModalOpen] = useState(false)

  useEffect(() => {
    if (value) {
      setCurrentValue(value)
      setPreviousValue(value)
    }
  }, [value])

  const handleChange = (e) => {
    setCurrentValue(e.target.value)
  }

  const treeValues = useMemo(() => {
    let newValue = ['/']
    if (currentValue) {
      newValue = currentValue.split(',').map(item => item.trim())
    }
    return newValue
  }, [currentValue])

  const handleOpenBrowseTree = () => {
    setIsTreeModalOpen(true)
    inputRef.current.blur()
  }

  const handleTreeSelect = (selectedPath) => {
    // Check if selectedPath is an array, join it into a string
    if (Array.isArray(selectedPath)) {
      selectedPath = selectedPath.join(',')
    }
    setCurrentValue(selectedPath)
    setIsTreeModalOpen(false)
    setIsEditable(true)
    inputRef.current.focus()
  }

  const handleTreeClose = () => {
    setIsTreeModalOpen(false)
    inputRef.current.focus()
  }

  useEffect(() => {
    if (isTreeModalOpen) {
      inputRef.current.blur()
    } else {
      inputRef.current.focus()
    }
  }, [isTreeModalOpen])


  const valid = regexPattern ? new RegExp(regexPattern).test(currentValue) : true
  const inputType = type === 'password' && isOpen ? 'text' : type

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
          isDisabled={isDisabled}
          ref={inputRef}
          value={currentValue}
          maxLength={maxLength}
          readOnly={!isEditable}
          onChange={handleChange}
          type={inputType}
          {...others}
        />
        {isEditable ? (
          <>
            {type === 'password' && (
              <RMIconButton
                isDisabled={isDisabled}
                icon={isOpen ? HiEyeOff : HiEye}
                aria-label={isOpen ? 'Mask password' : 'Reveal password'}
                onClick={onClickReveal}
              />
            )}
            {isTree && (
              <RMIconButton
                isDisabled={isDisabled}
                icon={HiFolder}
                aria-label="Browse Path"
                onClick={handleOpenBrowseTree} />
            )}
            <RMIconButton
              isDisabled={isDisabled}
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
              isDisabled={(isDisabled || !valid)}
              tooltip='Save'
              onClick={() => {
                setIsConfirmModalOpen(true)
              }}
            />
          </>
        ) : (
          <RMIconButton
            isDisabled={isDisabled}
            icon={HiPencilAlt}
            className={styles.btnEdit}
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
          title={`${confirmTitle}`}
          body={`${confirmBody} "${currentValue}"?`}
          onConfirmClick={() => {
            onSave(currentValue)
            setIsEditable(false)
            setIsConfirmModalOpen(false)
          }}
        />
      )}
      {isTreeModalOpen && (
        <TreeView
          isOpen={isTreeModalOpen}
          onClose={handleTreeClose}
          title="Browse Path"
          asModal={true}
          treeData={treeData}
          nodeToValue={nodeToValue}
          nodeToString={nodeToString}
          defaultValues={treeValues}
          onSave={handleTreeSelect}
        />
      )}
    </Flex>
  )
}

export default TextForm
