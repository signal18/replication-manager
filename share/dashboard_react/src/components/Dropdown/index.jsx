import { Button, Menu, MenuButton, MenuItem, MenuList } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import { HiChevronDown } from 'react-icons/hi'
import styles from './styles.module.scss'
import { useTheme } from '../../ThemeProvider'
import ConfirmModal from '../Modals/ConfirmModal'

function Dropdown({
  options,
  placeholder = 'Select option',
  selectedValue,
  width = '200px',
  onChange,
  confirmTitle,
  buttonClassName
}) {
  const [selectedOption, setSelectedOption] = useState(null)
  const [previousOption, setPreviousOption] = useState(null)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const { theme } = useTheme()

  useEffect(() => {
    if (options && selectedValue) {
      const option = options.find((opt) => opt.value === selectedValue)
      setSelectedOption(option)
      setPreviousOption(option)
    }
  }, [options, selectedValue])

  const handleOptionClick = (option) => {
    setSelectedOption(option)
    if (confirmTitle && option.value !== 'script') {
      openConfirmModal()
    } else {
      onChange(option?.value)
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
    <>
      <Menu variant='outline' placement='bottom-end'>
        <MenuButton
          width={width}
          as={Button}
          className={`${styles.menuButton} ${buttonClassName}`}
          rightIcon={<HiChevronDown fontSize={'1.5rem'} />}>
          {selectedOption ? selectedOption.name : placeholder}
        </MenuButton>
        <MenuList width={width}>
          {options.map((option, index) => (
            <MenuItem
              width={width}
              key={index}
              className={theme === 'light' ? styles.lightMenuItem : styles.darkMenuItem}
              onClick={() => handleOptionClick(option)}>
              {option.name}
            </MenuItem>
          ))}
        </MenuList>
      </Menu>
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={() => {
            closeConfirmModal('cancel')
          }}
          title={`${confirmTitle} ${selectedOption.value}`}
          onConfirmClick={() => {
            onChange(selectedOption.value)
            closeConfirmModal('')
          }}
        />
      )}
    </>
  )
}

export default Dropdown
