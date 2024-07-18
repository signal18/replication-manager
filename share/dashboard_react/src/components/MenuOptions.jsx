import React, { useEffect, useState } from 'react'
import { Menu, MenuButton, MenuList, MenuItem, IconButton, HStack, Spacer, useDisclosure } from '@chakra-ui/react'
import { HiChevronRight, HiDotsVertical } from 'react-icons/hi'

function MenuOptions({ options = [], placement = 'bottom', subMenuPlacement = 'bottom', ...rest }) {
  const [menuOptions, setMenuOptions] = useState([])
  const { isOpen, onOpen, onClose } = useDisclosure()

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

  return (
    <Menu colorScheme='blue' isOpen={isOpen} placement={placement} onClose={onClose} {...rest}>
      <MenuButton
        onClick={isOpen ? onClose : onOpen}
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
                      onClose()
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
                      onClose()
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
