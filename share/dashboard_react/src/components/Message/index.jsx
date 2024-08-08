import { Text } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'

function Message({ type = 'error', message }) {
  return <Text className={`${styles.message} ${type === 'error' ? styles.error : styles.success}`}>{message}</Text>
}

export default Message
