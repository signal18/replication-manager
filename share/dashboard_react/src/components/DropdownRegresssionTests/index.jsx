import { Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import Dropdown from '../Dropdown'
import RMButton from '../RMButton'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import ConfirmModal from '../Modals/ConfirmModal'
import { convertObjectToArrayForDropdown } from '../../utility/common'
import { runRemoteJobs } from '../../redux/clusterSlice'

function DropdownRegresssionTests({ clusterName }) {
  const dispatch = useDispatch()
  const {
    cluster: { clusterMaster }
  } = useSelector((state) => state)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [options, setOptions] = useState([])

  const [selectedOption, setSelectedOption] = useState({ name: 1, value: 1 })

  useEffect(() => {
    if (clusterMaster?.tests?.length > 0) {
      setOptions(convertObjectToArrayForDropdown(clusterMaster.tests))
    }
  }, [clusterMaster?.tests])

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }

  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
  }

  const runRegressionTests = () => {
    dispatch(runRegressionTests({ clusterName, thread: selectedOption.name }))
    closeConfirmModal()
  }
  return (
    <Flex className={styles.sysbenchContainer}>
      <Dropdown options={options} onChange={(value) => setSelectedOption(value)} label='Regression tests' />
      <RMButton type='button' onClick={openConfirmModal}>
        Run
      </RMButton>
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={`Confirm regression test for ${selectedOption.name}`}
          onConfirmClick={runRegressionTests}
        />
      )}
    </Flex>
  )
}

export default DropdownRegresssionTests
