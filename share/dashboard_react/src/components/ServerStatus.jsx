import React from 'react'
import TagPill from './TagPill'

function getStatus(state) {
  switch (state) {
    case 'SlaveErr':
      return { stateValue: 'SLAVE_ERROR', colorScheme: 'orange' }
    case 'SlaveLate':
      return { stateValue: 'SLAVE_LATE', colorScheme: 'yellow' }
    case 'RelayErr':
      return { stateValue: 'RELAY_ERROR', colorScheme: 'orange' }
    case 'RelayLate':
      return { stateValue: 'RELAY_LATE', colorScheme: 'yellow' }
    case 'StandAlone':
      return { stateValue: 'STANDALONE', colorScheme: 'gray' }
    case 'Master':
      return { stateValue: state.toUpperCase(), colorScheme: 'blue' }
    case 'Slave':
      return { stateValue: state.toUpperCase(), colorScheme: 'gray' }
    case 'Relay':
      return { stateValue: state.toUpperCase(), colorScheme: 'gray' }
    case 'Suspect':
      return { stateValue: state.toUpperCase(), colorScheme: 'orange' }
    case 'Failed':
      return { stateValue: state.toUpperCase(), colorScheme: 'red' }
    case 'AppRunning':
      return { stateValue: state.toUpperCase(), colorScheme: 'blue' }
    case 'AppWarning':
      return { stateValue: state.toUpperCase(), colorScheme: 'orange' }
    default:
      return { stateValue: state.toUpperCase(), colorScheme: 'gray' }
  }
}

function ServerStatus({ state, isVirtualMaster, isBlinking = false }) {
  const { stateValue, colorScheme } = state ? getStatus(state) : { stateValue: '', colorScheme: 'gray' }
  const isVirtual = isVirtualMaster ? '-VMaster' : ''

  return (
    <TagPill
      colorScheme={colorScheme}
      text={`${stateValue}${isVirtual}`}
      isBlinking={isBlinking && (colorScheme === 'red' || colorScheme === 'orange' || colorScheme === 'yellow')}
    />
  )
}

export default ServerStatus
