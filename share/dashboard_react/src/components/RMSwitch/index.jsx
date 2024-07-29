import { Flex, Switch, Text } from '@chakra-ui/react'
import React from 'react'

function RMSwitch({ id, onText = 'ON', offText = 'OFF', isChecked, size = 'lg', isDisabled, onChange }) {
  return (
    <Flex gap='2' align='center'>
      <Switch size={size} id={id} isChecked={isChecked} isDisabled={isDisabled} onChange={onChange} />
      <Text>{isChecked ? onText : offText}</Text>
    </Flex>
  )
}

export default RMSwitch
