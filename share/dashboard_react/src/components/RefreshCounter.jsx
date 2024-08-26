import React, { useState, useRef, useEffect } from 'react'
import { useDispatch } from 'react-redux'
import { HStack } from '@chakra-ui/react'
import { HiPlay, HiStop, HiRefresh, HiOutlineInformationCircle } from 'react-icons/hi'
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
import RMIconButton from './RMIconButton'
import NumberInput from './NumberInput'

function RefreshCounter({ clusterName }) {
  const defaultSeconds = getRefreshInterval() || AppSettings.DEFAULT_INTERVAL
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
      <RMIconButton icon={HiRefresh} tooltip='Reload manually' onClick={reloadManually} />

      {isPaused ? (
        <RMIconButton onClick={playInterval} icon={HiPlay} tooltip='Start auto reload' />
      ) : (
        <RMIconButton onClick={pauseInterval} icon={HiStop} tooltip='Pause auto reload' />
      )}

      {!isPaused && <NumberInput value={seconds} onChange={handleCountChange} />}

      <RMIconButton
        icon={HiOutlineInformationCircle}
        variant='ghost'
        style={{ backgroundColor: 'transparent', color: 'unset' }}
        tooltip={isPaused ? 'Auto reload is currently paused' : `Auto reload every ${seconds} seconds`}
      />
    </HStack>
  )
}

export default RefreshCounter
