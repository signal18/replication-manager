import { IconButton, useColorMode } from '@chakra-ui/react'
import { HiMoon, HiSun } from 'react-icons/hi'

function ThemeIcon() {
  const { colorMode, toggleColorMode } = useColorMode()

  return colorMode === 'light' ? (
    <IconButton onClick={toggleColorMode} icon={<HiMoon fontSize='1.5rem' />} size='sm' variant='filled' />
  ) : (
    <IconButton onClick={toggleColorMode} icon={<HiSun fontSize='1.5rem' />} size='sm' variant='filled' />
  )
}

export default ThemeIcon
