import React, { useEffect, useRef, useState } from 'react'
import { Menu, MenuButton, MenuList, MenuItem, IconButton, HStack, Spacer } from '@chakra-ui/react'
import { HiChevronRight, HiDotsVertical } from 'react-icons/hi'
import styles from './styles.module.scss'
import CustomIcon from '../Icons/CustomIcon'
import { acquireAutoReloadPause, releaseAutoReloadPause } from '../../utility/autoReloadPause'

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

  const pauseAutoReloadForMenu = () => {
    if (menuPauseRegisteredRef.current) {
      return
    }

    acquireAutoReloadPause()
    menuPauseRegisteredRef.current = true
  }

  const resumeAutoReloadFromMenu = () => {
    if (!menuPauseRegisteredRef.current) {
      return
    }

    releaseAutoReloadPause()

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
                      if (!subMenuOption.isDisabled && subMenuOption?.onClick) {
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
                      if (!option.isDisabled) {
                        option.onClick()
                      }
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
