import { Button, Menu, MenuButton, MenuItem, MenuList, useColorMode } from '@chakra-ui/react'
import React, { useState } from 'react'
import { HiChevronDown } from 'react-icons/hi'

function Dropdown({ options, placeholder = 'Select option', width = '200px', onChange }) {
  const { colorMode } = useColorMode()
  const [selectedOption, setSelectedOption] = useState(null)

  const handleOptionClick = (option) => {
    setSelectedOption(option)
    if (onChange) {
      onChange(option)
    }
  }
  const styles = {
    menuButton: {
      bg: colorMode === 'light' ? `blue.100` : `blue.800`,
      '&:hover': {
        bg: colorMode === 'light' ? `blue.100` : 'blue.800'
      }
    }
  }

  return (
    <Menu variant='outline'>
      <MenuButton width={width} as={Button} sx={styles.menuButton} rightIcon={<HiChevronDown fontSize={'1.5rem'} />}>
        {selectedOption ? selectedOption.name : placeholder}
      </MenuButton>
      <MenuList width={width}>
        {options.map((option, index) => (
          <MenuItem key={index} onClick={() => handleOptionClick(option)}>
            {option.name}
          </MenuItem>
        ))}
      </MenuList>
    </Menu>
  )
}

export default Dropdown
