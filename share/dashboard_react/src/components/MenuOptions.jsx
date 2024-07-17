import React, { useEffect, useState } from 'react'
import { Menu, MenuButton, MenuList, MenuItem, IconButton, HStack, Spacer } from '@chakra-ui/react'
import { HiChevronRight, HiDotsVertical } from 'react-icons/hi'

function MenuOptions({ options = [], placement = 'bottom', subMenuPlacement = 'bottom', ...rest }) {
  const [menuOptions, setMenuOptions] = useState([])
  const [isMenuOpen, setIsMenuOpen] = useState(false)

  useEffect(() => {
    if (options.length > 0) {
      setMenuOptions(options)
    }
  }, [options])

  const styles = {
    menuButton: {
      width: '32px',
      height: '32px',
      minWidth: '32px'
    }
  }

  const handleMenuClose = () => {
    setIsMenuOpen(false)
  }

  return (
    <Menu colorScheme='blue' isOpen={isMenuOpen} placement={placement} {...rest}>
      <MenuButton
        onClick={() => setIsMenuOpen(!isMenuOpen)}
        aria-label='Options'
        sx={styles.menuButton}
        as={IconButton}
        icon={<HiDotsVertical />}></MenuButton>
      <MenuList zIndex={3}>
        {menuOptions?.map((option, index) => {
          return option.subMenu ? (
            <Menu key={index} placement={subMenuPlacement}>
              <MenuItem as={MenuButton}>
                <HStack>
                  <span>{option.name}</span> <Spacer /> <HiChevronRight fontSize={'1.5rem'} />
                </HStack>
              </MenuItem>
              <MenuList zIndex={3}>
                {option.subMenu.map((subMenuOption, subIndex) => (
                  <MenuItem
                    onClick={() => {
                      subMenuOption.onClick()
                      handleMenuClose()
                    }}
                    key={subIndex}>
                    {subMenuOption.name}
                  </MenuItem>
                ))}
              </MenuList>
            </Menu>
          ) : (
            <MenuItem
              {...(option.onClick
                ? {
                    onClick: () => {
                      option.onClick()
                      handleMenuClose()
                    }
                  }
                : {})}>
              {option.name}
            </MenuItem>
          )
        })}
      </MenuList>
    </Menu>
  )
}

export default MenuOptions
