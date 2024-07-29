import { Button, Menu, MenuButton, MenuItem, MenuList } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import { HiChevronDown } from 'react-icons/hi'
import styles from './styles.module.scss'
import { useTheme } from '../../ThemeProvider'

function Dropdown({
  options,
  placeholder = 'Select option',
  selectedValue,
  width = '200px',
  onChange,
  askConfirmation = false
}) {
  const [selectedOption, setSelectedOption] = useState(null)
  const { theme } = useTheme()

  useEffect(() => {
    if (options && selectedValue) {
      const option = options.find((opt) => opt.value === selectedValue)
      setSelectedOption(option)
    }
  }, [options, selectedValue])

  const handleOptionClick = (option) => {
    if (!askConfirmation) {
      setSelectedOption(option)
    }
    if (onChange) {
      onChange(option)
    }
  }

  return (
    <Menu variant='outline' placement='bottom-end'>
      <MenuButton
        width={width}
        as={Button}
        className={styles.menuButton}
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
  )
}

export default Dropdown
