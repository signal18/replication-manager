import { Textarea } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'

function RMTextarea({ value, handleInputChange, rows = 10, cols = 100 }) {
  return (
    <Textarea
      className={styles.textarea}
      value={value}
      rows={rows}
      cols={cols}
      onChange={handleInputChange}
      size='sm'
    />
  )
}

export default RMTextarea
