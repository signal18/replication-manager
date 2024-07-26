import { HiMoon, HiSun } from 'react-icons/hi'
import { useTheme } from '../../ThemeProvider'
import IconButton from '../IconButton'

function ThemeIcon() {
  const { theme, toggleTheme } = useTheme()

  return theme === 'light' ? (
    <IconButton
      style={{ backgroundColor: 'transparent' }}
      onClick={toggleTheme}
      icon={HiMoon}
      iconFillColor='midnightblue'
      variant='filled'
      tooltip='Switch to dark mode'
    />
  ) : (
    <IconButton
      style={{ backgroundColor: 'transparent' }}
      onClick={toggleTheme}
      icon={HiSun}
      iconFillColor='yellow'
      variant='filled'
      tooltip='Switch to light mode'
    />
  )
}

export default ThemeIcon
