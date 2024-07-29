import React from 'react'
import { GrMysql } from 'react-icons/gr'
import { SiMariadbfoundation } from 'react-icons/si'
import IconButton from '../../IconButton'
import styles from './styles.module.scss'

function DBFlavourIcon({ dbFlavor, isBlocking }) {
  return dbFlavor === 'MariaDB' ? (
    <IconButton icon={SiMariadbfoundation} className={`${styles.dbFlavor} ${styles.whiteIcon}`} tooltip={dbFlavor} />
  ) : dbFlavor === 'MySQL' ? (
    <IconButton icon={GrMysql} className={`${styles.dbFlavor} ${styles.whiteIcon}`} tooltip={dbFlavor} />
  ) : null
}

export default DBFlavourIcon
