import React, { useEffect, useRef, useState } from 'react'
import { Menu, MenuButton, MenuList, MenuItem, IconButton, HStack, Spacer } from '@chakra-ui/react'
import { HiChevronRight, HiDotsVertical } from 'react-icons/hi'
import styles from './styles.module.scss'
import CustomIcon from '../Icons/CustomIcon'
import { AUTO_RELOAD_PAUSE_KEY } from '../../utility/autoReloadPause'

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

  useEffect(() => {
    setMenuOptions(options || [])
  }, [options])

  useEffect(() => {
    return () => {
      resumeAutoReloadFromMenu()
    }
  }, [])

  return (
    <Menu
      colorScheme={colorScheme}
      placement={placement}
      onOpen={pauseAutoReloadForMenu}
      onClose={resumeAutoReloadFromMenu}
      {...rest}>
      <MenuButton
        colorScheme={colorScheme}
        onClick={(event) => {
          if (event) {
            event.stopPropagation()
          }
        }}
        aria-label='Options'
        type='button'
        className={`${styles.menuButton} ${colorScheme === 'blue' ? styles.baseColor : ''} ${className}`}
        as={IconButton}
        icon={<HiDotsVertical />}></MenuButton>
      <MenuList
        className={styles.menuList}
        onClick={(event) => {
          if (event) {
            event.stopPropagation()
          }
        }}>
        {menuOptions?.map((option, index) => {
          return option.subMenu ? (
            <Menu key={index} placement={subMenuPlacement}>
              <MenuItem key={`item-${index}`} as={MenuButton} type='button'>
                <HStack>
                  <span>{option.name}</span> <Spacer /> <CustomIcon icon={HiChevronRight} />
                </HStack>
              </MenuItem>
              <MenuList
                key={`sub-${index}`}
                className={styles.menuList}
                onClick={(event) => {
                  if (event) {
                    event.stopPropagation()
                  }
                }}>
                {option.subMenu.map((subMenuOption, subIndex) => (
                  <MenuItem
                    onClick={(event) => {
                      if (event) {
                        event.stopPropagation()
                      }
                      if (subMenuOption?.onClick) {
                        subMenuOption.onClick()
                      }
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
