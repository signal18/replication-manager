import React, { useEffect, useRef, useState } from 'react'
import { Menu, MenuButton, MenuList, MenuItem, IconButton, HStack, Spacer, useDisclosure } from '@chakra-ui/react'
import { HiChevronRight, HiDotsVertical } from 'react-icons/hi'
import styles from './styles.module.scss'
import CustomIcon from '../Icons/CustomIcon'

const AUTO_RELOAD_PAUSE_KEY = 'pause_auto_reload'
const MENU_PAUSE_TOKEN = 'menu-options'
const MENU_PAUSE_COUNT_KEY = 'pause_auto_reload_menu_count'

function MenuOptions({
  options = [],
  placement = 'bottom',
  colorScheme = 'blue',
  subMenuPlacement = 'bottom',
  className,
  ...rest
}) {
  const [menuOptions, setMenuOptions] = useState([])
  const { isOpen, onOpen, onClose } = useDisclosure()
  const menuPauseRegisteredRef = useRef(false)

  const getMenuPauseCount = () => {
    const pauseCount = parseInt(localStorage.getItem(MENU_PAUSE_COUNT_KEY) || '0', 10)
    return Number.isNaN(pauseCount) ? 0 : pauseCount
  }

  const pauseAutoReloadForMenu = () => {
    if (menuPauseRegisteredRef.current) {
      return
    }

    const currentPauseState = localStorage.getItem(AUTO_RELOAD_PAUSE_KEY)
    const currentPauseCount = getMenuPauseCount()

    localStorage.setItem(MENU_PAUSE_COUNT_KEY, String(currentPauseCount + 1))
    menuPauseRegisteredRef.current = true

    if (!currentPauseState) {
      localStorage.setItem(AUTO_RELOAD_PAUSE_KEY, MENU_PAUSE_TOKEN)
    }
  }

  const resumeAutoReloadFromMenu = () => {
    if (!menuPauseRegisteredRef.current) {
      return
    }

    const currentPauseCount = getMenuPauseCount()
    const nextPauseCount = Math.max(0, currentPauseCount - 1)

    if (nextPauseCount === 0) {
      localStorage.removeItem(MENU_PAUSE_COUNT_KEY)

      if (localStorage.getItem(AUTO_RELOAD_PAUSE_KEY) === MENU_PAUSE_TOKEN) {
        localStorage.removeItem(AUTO_RELOAD_PAUSE_KEY)
      }
    } else {
      localStorage.setItem(MENU_PAUSE_COUNT_KEY, String(nextPauseCount))
    }

    menuPauseRegisteredRef.current = false
  }

  const handleMenuClose = () => {
    onClose()
    resumeAutoReloadFromMenu()
  }

  const handleMenuToggle = (event) => {
    if (event) {
      event.stopPropagation()
    }

    if (isOpen) {
      handleMenuClose()
      return
    }

    pauseAutoReloadForMenu()
    onOpen()
  }

  useEffect(() => {
    setMenuOptions(options || [])
  }, [options])

  useEffect(() => {
    return () => {
      resumeAutoReloadFromMenu()
    }
  }, [])

  return (
    <Menu colorScheme={colorScheme} isOpen={isOpen} placement={placement} onClose={handleMenuClose} {...rest}>
      <MenuButton
        colorScheme={colorScheme}
        onClick={handleMenuToggle}
        aria-label='Options'
        type='button'
        className={`${styles.menuButton} ${colorScheme === 'blue' ? styles.baseColor : ''} ${className}`}
        as={IconButton}
        icon={<HiDotsVertical />}></MenuButton>
      <MenuList className={styles.menuList}>
        {menuOptions?.map((option, index) => {
          return option.subMenu ? (
            <Menu key={index} placement={subMenuPlacement}>
              <MenuItem
                key={`item-${index}`}
                as={MenuButton}
                type='button'>
                <HStack>
                  <span>{option.name}</span> <Spacer /> <CustomIcon icon={HiChevronRight} />
                </HStack>
              </MenuItem>
              <MenuList key={`sub-${index}`} className={styles.menuList}>
                {option.subMenu.map((subMenuOption, subIndex) => (
                  <MenuItem
                    onClick={(event) => {
                      if (event) {
                        event.stopPropagation()
                      }
                      subMenuOption.onClick()
                      handleMenuClose()
                    }}
                    {...(subMenuOption.isDisabled ? { isDisabled: subMenuOption.isDisabled } : {})}
                    key={subIndex}>
                    {subMenuOption.name}
                  </MenuItem>
                ))}
              </MenuList>
            </Menu>
          ) : (
            <MenuItem
              key={index}
              {...(option.onClick
                ? {
                    onClick: (event) => {
                      if (event) {
                        event.stopPropagation()
                      }
                      option.onClick()
                      handleMenuClose()
                    }
                  }
                : {})}
              {...(option.isDisabled ? { isDisabled: option.isDisabled } : {})}
              >
              {option.name}
            </MenuItem>
          )
        })}
      </MenuList>
    </Menu>
  )
}

export default MenuOptions
