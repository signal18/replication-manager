import React, { useEffect } from 'react'
import { IconButton, useColorMode } from '@chakra-ui/react'
import { HiMoon, HiSun } from 'react-icons/hi'
import { setTheme } from '../../redux/commonSlice'
import { useDispatch } from 'react-redux'

function ThemeIcon({ theme }) {
  const dispatch = useDispatch()
  const { colorMode, toggleColorMode } = useColorMode()

  useEffect(() => {
    dispatch(setTheme({ theme: colorMode }))
  }, [colorMode])
  return theme === 'light' ? (
    <IconButton onClick={toggleColorMode} icon={<HiMoon fontSize='1.5rem' />} size='sm' variant='filled' />
  ) : (
    <IconButton onClick={toggleColorMode} icon={<HiSun fontSize='1.5rem' />} size='sm' variant='filled' />
  )
}

export default ThemeIcon
