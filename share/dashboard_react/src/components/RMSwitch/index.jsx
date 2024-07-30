import { Flex, Spinner, Switch, Text } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'

function RMSwitch({ id, onText = 'ON', offText = 'OFF', isChecked, size = 'lg', isDisabled, onChange, loading }) {
  return (
    <Flex className={styles.switchContainer} align='center'>
      <Switch size={size} id={id} isChecked={isChecked} isDisabled={isDisabled} onChange={onChange} />
      <Text className={`${styles.text} ${isChecked ? styles.green : styles.red}`}>{isChecked ? onText : offText}</Text>
      {loading && <Spinner />}
    </Flex>
  )
}

export default RMSwitch
