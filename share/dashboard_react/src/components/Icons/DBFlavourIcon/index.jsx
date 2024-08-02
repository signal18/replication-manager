import React from 'react'
import { GrMysql } from 'react-icons/gr'
import { SiMariadbfoundation } from 'react-icons/si'
import RMIconButton from '../../RMIconButton'
import styles from './styles.module.scss'

function DBFlavourIcon({ dbFlavor, isBlocking, from }) {
  return dbFlavor === 'MariaDB' ? (
    <RMIconButton
      icon={SiMariadbfoundation}
      className={`${styles.dbFlavor} ${isBlocking || from === 'gridView' ? styles.whiteIcon : styles.primaryIcon}`}
      tooltip={dbFlavor}
    />
  ) : dbFlavor === 'MySQL' ? (
    <RMIconButton
      icon={GrMysql}
      className={`${styles.dbFlavor} ${isBlocking || from === 'gridView' ? styles.whiteIcon : styles.primaryIcon}`}
      tooltip={dbFlavor}
    />
  ) : null
}

export default DBFlavourIcon
