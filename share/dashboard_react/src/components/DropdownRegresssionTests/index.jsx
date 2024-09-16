import { Flex } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import Dropdown from '../Dropdown'
import RMButton from '../RMButton'
import styles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import ConfirmModal from '../Modals/ConfirmModal'
import { convertObjectToArrayForDropdown } from '../../utility/common'
import { getMonitoredData, runRegressionTests } from '../../redux/clusterSlice'

function DropdownRegresssionTests({ clusterName }) {
  const dispatch = useDispatch()
  const {
    cluster: { monitor }
  } = useSelector((state) => state)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [options, setOptions] = useState([])

  const [selectedOption, setSelectedOption] = useState({ name: 1, value: 1 })

  useEffect(() => {
    console.log
    if (monitor?.tests?.length > 0) {
      setOptions(convertObjectToArrayForDropdown(monitor.tests))
    } else {
      dispatch(getMonitoredData({}))
    }
  }, [monitor?.tests])

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }

  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
  }

  const runRegressionTest = () => {
    dispatch(runRegressionTests({ clusterName, testName: selectedOption.name }))
    closeConfirmModal()
  }
  return (
    <Flex className={styles.regressionTestContainer}>
      <Dropdown
        classNamePrefix='run-tests'
        className={styles.dropdown}
        options={options}
        onChange={(value) => setSelectedOption(value)}
        label='Regression tests'
      />
      <RMButton type='button' onClick={openConfirmModal}>
        Run
      </RMButton>
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={`Confirm regression test for ${selectedOption.name}`}
          onConfirmClick={runRegressionTest}
        />
      )}
    </Flex>
  )
}

export default DropdownRegresssionTests
