import { Icon } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'

function CustomIcon({ icon, color, fontSize = '1.5rem', fill, className }) {
  return <Icon fontSize={fontSize} className={`${className} ${styles[color]}`} as={icon} fill={fill} />
}

export default CustomIcon
