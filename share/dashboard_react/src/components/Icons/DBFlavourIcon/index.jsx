import React from 'react'
import { GrMysql } from 'react-icons/gr'
import { SiMariadbfoundation } from 'react-icons/si'
import RMIconButton from '../../RMIconButton'
import styles from './styles.module.scss'
import { useTheme } from '../../../ThemeProvider'

function DBFlavourIcon({ dbFlavor, isBlocking, from }) {
  const { theme } = useTheme()
  return dbFlavor === 'MariaDB' ? (
    <RMIconButton
      icon={SiMariadbfoundation}
      className={`${styles.dbFlavor} ${isBlocking || from === 'gridView' || theme === 'dark' ? styles.whiteIcon : styles.primaryIcon}`}
      tooltip={dbFlavor}
    />
  ) : dbFlavor === 'MySQL' ? (
    <RMIconButton
      icon={GrMysql}
      className={`${styles.dbFlavor} ${isBlocking || from === 'gridView' || theme === 'dark' ? styles.whiteIcon : styles.primaryIcon}`}
      tooltip={dbFlavor}
    />
  ) : null
}

export default DBFlavourIcon
