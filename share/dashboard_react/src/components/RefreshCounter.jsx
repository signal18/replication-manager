import React, { useState, useRef, useEffect } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { HStack, useNumberInput, Input, IconButton, Tooltip } from '@chakra-ui/react'
import {
  HiOutlinePlusCircle,
  HiOutlineMinusCircle,
  HiPlay,
  HiStop,
  HiRefresh,
  HiOutlineInformationCircle
} from 'react-icons/hi'
import { setRefreshInterval } from '../redux/clusterSlice'
import { getRefreshInterval } from '../utility/common'
import { AppSettings } from '../AppSettings'

function RefreshCounter(props) {
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

  const {
    cluster: { refreshInterval }
  } = useSelector((state) => state)

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

  return (
    <HStack spacing='4'>
      <Tooltip label='Reload manually' aria-label='A tooltip'>
        <IconButton icon={<HiRefresh fontSize='1.5rem' />} size='sm' />
      </Tooltip>
      {isPaused ? (
        <Tooltip label='Start auto reload' aria-label='A tooltip'>
          <IconButton onClick={playInterval} icon={<HiPlay />} size='sm' />
        </Tooltip>
      ) : (
        <Tooltip label='Pause auto reload' aria-label='A tooltip'>
          <IconButton onClick={pauseInterval} icon={<HiStop fontSize='1.5rem' />} size='sm' />
        </Tooltip>
      )}

      {!isPaused && (
        <HStack spacing='3'>
          <IconButton {...dec} icon={<HiOutlineMinusCircle fontSize='1.5rem' />} size='sm' aria-label='Decrement' />
          <Input {...input} width='75px' size='sm' ref={inputRef} />
          <IconButton {...inc} icon={<HiOutlinePlusCircle fontSize='1.5rem' />} size='sm' aria-label='Increment' />
        </HStack>
      )}
      <Tooltip
        label={isPaused ? 'Auto reload is currently paused' : `Auto reload every ${seconds} seconds`}
        aria-label='A tooltip'>
        <IconButton icon={<HiOutlineInformationCircle fontSize='1.5rem' />} size='sm' variant='ghost' />
      </Tooltip>
    </HStack>
  )
}

export default RefreshCounter
