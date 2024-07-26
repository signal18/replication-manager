import React, { useEffect, useState } from 'react'
import { Menu, MenuButton, MenuList, MenuItem, IconButton, HStack, Spacer, useDisclosure } from '@chakra-ui/react'
import { HiChevronRight, HiDotsVertical } from 'react-icons/hi'
import styles from './styles.module.scss'
import CustomIcon from '../Icons/CustomIcon'

function MenuOptions({
  options = [],
  placement = 'bottom',
  colorScheme = 'blue',
  subMenuPlacement = 'bottom',
  ...rest
}) {
  const [menuOptions, setMenuOptions] = useState([])
  const { isOpen, onOpen, onClose } = useDisclosure()

  useEffect(() => {
    if (options.length > 0) {
      setMenuOptions(options)
    }
  }, [options])

  return (
    <Menu colorScheme={colorScheme} isOpen={isOpen} placement={placement} onClose={onClose} {...rest}>
      <MenuButton
        colorScheme={colorScheme}
        onClick={isOpen ? onClose : onOpen}
        aria-label='Options'
        className={styles.menuButton}
        as={IconButton}
        icon={<HiDotsVertical />}></MenuButton>
      <MenuList className={styles.menuList}>
        {menuOptions?.map((option, index) => {
          return option.subMenu ? (
            <Menu key={index} placement={subMenuPlacement}>
              <MenuItem as={MenuButton}>
                <HStack>
                  <span>{option.name}</span> <Spacer /> <CustomIcon icon={HiChevronRight} />
                </HStack>
              </MenuItem>
              <MenuList className={styles.menuList}>
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
