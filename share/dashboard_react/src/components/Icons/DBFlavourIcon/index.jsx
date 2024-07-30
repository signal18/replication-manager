import React from 'react'
import { GrMysql } from 'react-icons/gr'
import { SiMariadbfoundation } from 'react-icons/si'
import IconButton from '../../IconButton'
import styles from './styles.module.scss'

function DBFlavourIcon({ dbFlavor, isBlocking, from }) {
  return dbFlavor === 'MariaDB' ? (
    <IconButton
      icon={SiMariadbfoundation}
      className={`${styles.dbFlavor} ${isBlocking || from === 'gridView' ? styles.whiteIcon : styles.primaryIcon}`}
      tooltip={dbFlavor}
    />
  ) : dbFlavor === 'MySQL' ? (
    <IconButton
      icon={GrMysql}
      className={`${styles.dbFlavor} ${isBlocking || from === 'gridView' ? styles.whiteIcon : styles.primaryIcon}`}
      tooltip={dbFlavor}
    />
  ) : null
}

export default DBFlavourIcon
