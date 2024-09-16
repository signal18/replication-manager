import { Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import Dropdown from '../Dropdown'
import RMButton from '../RMButton'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import ConfirmModal from '../Modals/ConfirmModal'
import { runSysBench } from '../../redux/clusterSlice'

function DropdownSysbench({ clusterName }) {
  const dispatch = useDispatch()
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [options, setOptions] = useState([
    { name: 1, value: 1 },
    { name: 4, value: 4 },
    { name: 8, value: 8 },
    { name: 16, value: 16 },
    { name: 32, value: 32 },
    { name: 64, value: 64 },
    { name: 128, value: 128 }
  ])
  const [selectedOption, setSelectedOption] = useState({ name: 1, value: 1 })

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }

  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
  }

  const runSysbench = () => {
    dispatch(runSysBench({ clusterName, thread: selectedOption.value }))
    closeConfirmModal()
  }
  return (
    <Flex className={styles.sysbenchContainer}>
      <Dropdown options={options} onChange={(value) => setSelectedOption(value)} label='Sysbench tests' />
      <RMButton type='button' onClick={openConfirmModal}>
        Run
      </RMButton>
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={`Confirm sysbench run for thread ${selectedOption.name}`}
          onConfirmClick={runSysbench}
        />
      )}
    </Flex>
  )
}

export default DropdownSysbench
