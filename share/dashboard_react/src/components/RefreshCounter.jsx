import React, { useState, useRef, useEffect } from 'react'
import { useDispatch } from 'react-redux'
import { HStack, useNumberInput, Input } from '@chakra-ui/react'
import {
  HiOutlinePlusCircle,
  HiOutlineMinusCircle,
  HiPlay,
  HiStop,
  HiRefresh,
  HiOutlineInformationCircle
} from 'react-icons/hi'
import {
  getClusterAlerts,
  getClusterData,
  getClusterMaster,
  getClusterProxies,
  getClusterServers,
  pauseAutoReload,
  setRefreshInterval
} from '../redux/clusterSlice'
import { getRefreshInterval } from '../utility/common'
import { AppSettings } from '../AppSettings'
import IconButton from './IconButton'

function RefreshCounter({ clusterName }) {
  const defaultSeconds = getRefreshInterval() || AppSettings.DEFAULT_INTERVAL
  const inputRef = useRef(null)
  const [seconds, setSeconds] = useState(defaultSeconds)
  const [isPaused, setIsPaused] = useState(false)
  const dispatch = useDispatch()

  useEffect(() => {
    const currentInterval = getRefreshInterval()

    if (!currentInterval) {
      setSeconds(currentInterval)
      dispatch(setRefreshInterval({ interval: AppSettings.DEFAULT_INTERVAL }))
    }
  }, [])

  useEffect(() => {
    dispatch(pauseAutoReload({ isPaused }))
  }, [isPaused])

  const handleCountChange = (value, number) => {
    setSeconds(number)
    dispatch(setRefreshInterval({ interval: number }))
  }

  const { getInputProps, getIncrementButtonProps, getDecrementButtonProps } = useNumberInput({
    step: 1,
    defaultValue: seconds,
    min: 2,
    max: 120,
    onChange: (valueAsString, valueAsNumber) => handleCountChange(valueAsString, valueAsNumber)
  })

  const inc = getIncrementButtonProps()
  const dec = getDecrementButtonProps()
  const input = getInputProps()

  const playInterval = () => {
    setIsPaused(false)
  }

  const pauseInterval = () => {
    setIsPaused(true)
  }

  const reloadManually = () => {
    if (clusterName) {
      dispatch(getClusterData({ clusterName }))
      dispatch(getClusterAlerts({ clusterName }))
      dispatch(getClusterMaster({ clusterName }))
      dispatch(getClusterServers({ clusterName }))
      dispatch(getClusterProxies({ clusterName }))
    }
  }

  return (
    <HStack spacing='4'>
      <IconButton icon={HiRefresh} tooltip='Reload manually' onClick={reloadManually} />

      {isPaused ? (
        <IconButton onClick={playInterval} icon={HiPlay} tooltip='Start auto reload' />
      ) : (
        <IconButton onClick={pauseInterval} icon={HiStop} tooltip='Pause auto reload' />
      )}

      {!isPaused && (
        <HStack spacing='3'>
          <IconButton {...dec} icon={HiOutlineMinusCircle} aria-label='Decrement' />
          <Input {...input} width='75px' size='sm' ref={inputRef} />
          <IconButton {...inc} icon={HiOutlinePlusCircle} aria-label='Increment' />
        </HStack>
      )}

      <IconButton
        icon={HiOutlineInformationCircle}
        variant='ghost'
        style={{ backgroundColor: 'transparent', color: 'unset' }}
        tooltip={isPaused ? 'Auto reload is currently paused' : `Auto reload every ${seconds} seconds`}
      />
    </HStack>
  )
}

export default RefreshCounter
