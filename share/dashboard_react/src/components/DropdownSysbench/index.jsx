import { Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import Dropdown from '../Dropdown'
import RMButton from '../RMButton'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import ConfirmModal from '../Modals/ConfirmModal'
import { runSysBench, cleanupSysBench } from '../../redux/clusterSlice'

const SYSBENCH_TESTS = [
  { name: 'oltp_read_write', value: 'oltp_read_write' },
  { name: 'oltp_read_only', value: 'oltp_read_only' },
  { name: 'oltp_update_index', value: 'oltp_update_index' },
  { name: 'oltp_update_non_index', value: 'oltp_update_non_index' },
  { name: 'tpcc', value: 'tpcc' },
]

const THREAD_OPTIONS = [
  { name: '1-2xCPU', value: 0 },
  { name: 1, value: 1 },
  { name: 4, value: 4 },
  { name: 8, value: 8 },
  { name: 16, value: 16 },
  { name: 32, value: 32 },
  { name: 64, value: 64 },
  { name: 128, value: 128 }
]

const TIME_OPTIONS = [
  { name: '100s', value: 100 },
  { name: '5 min', value: 300 },
  { name: '10 min', value: 600 },
  { name: '15 min', value: 900 },
  { name: '30 min', value: 1800 },
  { name: '1 hour', value: 3600 }
]

function DropdownSysbench({ clusterName }) {
  const dispatch = useDispatch()
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [isCleanupConfirmOpen, setIsCleanupConfirmOpen] = useState(false)
  const [selectedThread, setSelectedThread] = useState(THREAD_OPTIONS[0])
  const [selectedTest, setSelectedTest] = useState(SYSBENCH_TESTS[0])
  const [selectedTime, setSelectedTime] = useState(TIME_OPTIONS[0])

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }

  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
  }

  const runSysbench = () => {
    dispatch(runSysBench({ clusterName, thread: selectedThread.value, test: selectedTest.value, time: selectedTime.value }))
    closeConfirmModal()
  }
  return (
    <Flex className={styles.sysbenchContainer}>
      <Dropdown options={SYSBENCH_TESTS} onChange={(value) => setSelectedTest(value)} label='Sysbench test' selectedValue={selectedTest.value} />
      <Dropdown options={THREAD_OPTIONS} onChange={(value) => setSelectedThread(value)} label='Threads' selectedValue={selectedThread.value} />
      <Dropdown options={TIME_OPTIONS} onChange={(value) => setSelectedTime(value)} label='Time' selectedValue={selectedTime.value} />
      <RMButton type='button' onClick={openConfirmModal}>
        Run
      </RMButton>
      <RMButton type='button' colorScheme='red' onClick={() => setIsCleanupConfirmOpen(true)}>
        Cleanup
      </RMButton>
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={selectedThread.value === 0
            ? `Run ${selectedTest.name} for ${selectedTime.name}, scaling threads from 1 to 2×CPU cores?`
            : `Run ${selectedTest.name} with ${selectedThread.name} threads for ${selectedTime.name}?`}
          onConfirmClick={runSysbench}
        />
      )}
      {isCleanupConfirmOpen && (
        <ConfirmModal
          isOpen={isCleanupConfirmOpen}
          closeModal={() => setIsCleanupConfirmOpen(false)}
          title={`Drop ${selectedTest.name} benchmark tables?`}
          onConfirmClick={() => {
            dispatch(cleanupSysBench({ clusterName, test: selectedTest.value }))
            setIsCleanupConfirmOpen(false)
          }}
        />
      )}
    </Flex>
  )
}

export default DropdownSysbench
