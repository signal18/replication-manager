import { Flex } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import styles from './styles.module.scss'
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
  isSearchable = false,
  isMenuPortalTarget = true
}) {
  const [selectedOption, setSelectedOption] = useState(null)
  const [previousOption, setPreviousOption] = useState(null)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)

  useEffect(() => {
    if (options && selectedValue) {
      const option = options.find((opt) => opt.value == selectedValue || opt.name === selectedValue)
      if (option) {
        setSelectedOption(option)
        setPreviousOption(option)
      }
    }
  }, [options, selectedValue])

  const handleChange = (option) => {
    setSelectedOption(option)
    if (confirmTitle && option.value !== 'script') {
      openConfirmModal()
    } else {
      onChange(option)
    }
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
        {...(isMenuPortalTarget ? { menuPortalTarget: document.body } : {})}
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
