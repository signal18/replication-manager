import React, { useState, useRef } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import {
  NumberInput,
  NumberInputField,
  NumberInputStepper,
  NumberIncrementStepper,
  NumberDecrementStepper,
  HStack,
  Text,
  useNumberInput,
  Input,
  Button,
  IconButton,
  Tooltip,
  Icon
} from '@chakra-ui/react'
import {
  HiOutlinePlusCircle,
  HiOutlineMinusCircle,
  HiPlay,
  HiStop,
  HiRefresh,
  HiOutlineInformationCircle
} from 'react-icons/hi'
import { setRefreshInterval } from '../../redux/clusterSlice'

function RefreshCounter(props) {
  const inputRef = useRef(null)
  const [seconds, setSeconds] = useState(4)
  const [isPaused, setIsPaused] = useState(false)
  const dispatch = useDispatch()

  const {
    cluster: { refreshInterval }
  } = useSelector((state) => state)

  const handleCountChange = (value, number) => {
    setSeconds(number)
    dispatch(setRefreshInterval({ interval: number }))
  }

  const { getInputProps, getIncrementButtonProps, getDecrementButtonProps } = useNumberInput({
    step: 1,
    defaultValue: refreshInterval || 4,
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
        <IconButton icon={<HiRefresh fontSize='1.5rem' />} size='sm' variant='outline' />
      </Tooltip>
      {isPaused ? (
        <Tooltip label='Start auto reload' aria-label='A tooltip'>
          <IconButton onClick={playInterval} icon={<HiPlay fontSize='1.5rem' />} size='sm' variant='outline' />
        </Tooltip>
      ) : (
        <Tooltip label='Pause auto reload' aria-label='A tooltip'>
          <IconButton onClick={pauseInterval} icon={<HiStop fontSize='1.5rem' />} size='sm' variant='outline' />
        </Tooltip>
      )}

      {!isPaused && (
        <HStack spacing='3'>
          <IconButton
            {...dec}
            icon={<HiOutlineMinusCircle fontSize='1.5rem' />}
            size='sm'
            aria-label='Decrement'
            variant='outline'
          />
          <Input {...input} width='75px' size='sm' ref={inputRef} />
          <IconButton
            {...inc}
            icon={<HiOutlinePlusCircle fontSize='1.5rem' />}
            size='sm'
            aria-label='Increment'
            variant='outline'
          />
        </HStack>
      )}
      <Tooltip
        label={isPaused ? 'Auto reload is currently paused' : `Auto reload every ${seconds} seconds`}
        aria-label='A tooltip'>
        {/* <Icon as={HiInformationCircle} fontSize='1.5rem' /> */}
        <IconButton icon={<HiOutlineInformationCircle fontSize='1.5rem' />} size='sm' variant='ghost' />
      </Tooltip>
      {/* <Text fontSize='sm'>{`Auto reload every ${seconds} seconds`}</Text> */}
    </HStack>
  )
}

export default RefreshCounter
