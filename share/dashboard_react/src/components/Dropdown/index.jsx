import { Box, Button, Flex, Menu, MenuButton, MenuItem, MenuList, Text } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import { HiChevronDown } from 'react-icons/hi'
import styles from './styles.module.scss'
import { useTheme } from '../../ThemeProvider'
import ConfirmModal from '../Modals/ConfirmModal'
import Select from 'react-select'

function Dropdown({
  id,
  options,
  placeholder = 'Select',
  label,
  selectedValue,
  className,
  onChange,
  confirmTitle,
  isSearchable = false
}) {
  const [selectedOption, setSelectedOption] = useState(null)
  const [previousOption, setPreviousOption] = useState(null)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const { theme } = useTheme()

  useEffect(() => {
    if (options && selectedValue) {
      const option = options.find((opt) => opt.value == selectedValue)
      setSelectedOption(option)
      setPreviousOption(option)
    }
  }, [options, selectedValue])

  const handleChange = (option) => {
    setSelectedOption(option)
    if (confirmTitle && option.value !== 'script') {
      openConfirmModal()
    } else {
      onChange(option)
    }
    setIsConfirmModalOpen(false)
  }

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }
  const closeConfirmModal = (action) => {
    if (action === 'cancel') {
      setSelectedOption(previousOption)
    }
    setIsConfirmModalOpen(false)
  }

  return (
    <Flex className={styles.selectFormContainer}>
      {label && (
        <label className={styles.label} htmlFor={id}>
          {label}
        </label>
      )}
      <Select
        id={id}
        className={`${styles.select}  ${className}`}
        classNamePrefix='rm-select'
        getOptionLabel={(option) => option.name || option.label}
        value={selectedOption}
        onChange={handleChange}
        options={options}
        isSearchable={isSearchable}
        placeholder={placeholder}
      />
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={() => {
            closeConfirmModal('cancel')
          }}
          title={`${confirmTitle} ${selectedOption.name}`}
          onConfirmClick={() => {
            onChange(selectedOption.value || selectedOption.name)
            closeConfirmModal('')
          }}
        />
      )}
    </Flex>
  )
}

export default Dropdown
