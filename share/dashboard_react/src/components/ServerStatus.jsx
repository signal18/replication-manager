import React, { useEffect, useState } from 'react'
import TagPill from './TagPill'

function ServerStatus({ state, isVirtualMaster, isBlinking = false }) {
  const [isVirtual, setIsVirtual] = useState(isVirtualMaster ? '-VMaster' : '')
  const [colorScheme, setColorScheme] = useState('gray')
  const [stateValue, setStateValue] = useState(state.toUpperCase())

  useEffect(() => {
    if (state) {
      setStateValue(state.toUpperCase())
      switch (state) {
        case 'SlaveErr':
          setStateValue('SLAVE_ERROR')
          setColorScheme('orange')
          break
        case 'SlaveLate':
          setStateValue('SLAVE_LATE')
          setColorScheme('yellow')
          break
        case 'StandAlone':
          setStateValue('STANDALONE')
          setColorScheme('gray')
          break
        case 'Master':
          setColorScheme('blue')
          break
        case 'Slave':
          setColorScheme('gray')
          break
        case 'Suspect':
          setColorScheme('orange')
          break
        case 'Failed':
          setColorScheme('red')
          break
        default:
          setStateValue(state.toUpperCase())
          break
      }
    }
  }, [state])

  return (
    <TagPill
      colorScheme={colorScheme}
      text={`${stateValue}${isVirtual}`}
      isBlinking={isBlinking && (colorScheme === 'red' || colorScheme === 'orange' || colorScheme === 'yellow')}
    />
  )
}

export default ServerStatus
