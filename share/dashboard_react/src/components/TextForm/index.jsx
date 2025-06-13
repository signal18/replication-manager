import { Box, Flex, Input, Spinner } from '@chakra-ui/react'
import React, { useEffect, useState, useRef, useMemo } from 'react'
import { HiCheck, HiFolder, HiPencilAlt, HiX } from 'react-icons/hi'
import styles from './styles.module.scss'
import RMIconButton from '../RMIconButton'
import ConfirmModal from '../Modals/ConfirmModal'
import TreeView from '../Modals/TreeView/TreeView'

function TextForm({ onSave, id, label, value, loading, maxLength = 120, className, direction, confirmTitle, regexPattern, isDisabled, isTree = false, treeData, nodeToValue, nodeToString, ...others }) {
  const [isEditable, setIsEditable] = useState(false)
  const inputRef = useRef(null)

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
          {...others}
        />
        {isEditable ? (
          <>
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
          title={`${confirmTitle} ${currentValue}`}
          onConfirmClick={() => {
            onSave(currentValue)
            setIsEditable(false)
            setIsConfirmModalOpen(false)
          }}
        />
      )}
      { isTreeModalOpen && (
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
