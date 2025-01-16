import {
  FormControl,
  FormErrorMessage,
  FormLabel,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Stack
} from '@chakra-ui/react'
import React, { useEffect, useMemo, useState } from 'react'
import { useDispatch } from 'react-redux'
import { addCluster } from '../../redux/globalClustersSlice'
import Dropdown from '../Dropdown'
import RMButton from '../RMButton'
import { useTheme } from '../../ThemeProvider'
import parentStyles from './styles.module.scss'
import TableType2 from '../TableType2'

function NewClusterModal({ plans, orchestrators, defaultOrchestrator, isOpen, closeModal }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const [orchestratorOptions, setOrchestratorOptions] = useState([])
  const [planOptions, setPlanOptions] = useState([])
  const [planDetails, setPlanDetails] = useState(null)
  const [clusterName, setClusterName] = useState('')
  const [orchestrator, setOrchestrator] = useState('')
  const [plan, setPlan] = useState('')
  const [clusterNameError, setClusterNameError] = useState('')
  const [orchestratorError, setOrchestratorError] = useState('')
  const [planError, setPlanError] = useState('')

  const handleCreateNewCluster = () => {
    setClusterNameError('')
    setOrchestratorError('')

    if (!clusterName) {
      setClusterNameError('ClusterName is required')
      return
    }

    if (!orchestrator || orchestrator === 0) {
      setOrchestratorError('Orchestrator is required')
      return
    }

    dispatch(addCluster({ clusterName, formdata: { orchestrator, plan } }))
    closeModal()
  }

  const getOrchestatorOptions = (orcs = []) => [{ name: "No Orchestrator", value: '' } ,...orcs?.filter((obj) => obj.available).map((obj) => ({ name: obj.name, value: obj.name }))]
  const getPlanOptions = (plist = []) => [{ name: "No Plan", value: '' } ,...plist?.map((obj) => ({ name: obj.plan, value: obj.plan }))]

  const onPlanChange = (option) => {
    setPlan(option.value)
    setPlanDetails(plans.find((obj) => obj.plan === option.value))
    console.log(plans,option)
  }

  useEffect(() => {
    if (orchestrators) setOrchestratorOptions(getOrchestatorOptions(orchestrators));
    if (plans) setPlanOptions(getPlanOptions(plans));
  },[plans,orchestrators])

  const dataObject = useMemo(() => planDetails ? [
    {
      key: 'Plan',
      value: planDetails?.plan
    },
    {
      key: 'DB Memory',
      value: planDetails?.dbmemory
    },
    {
      key: 'DB Core(s)',
      value: planDetails?.dbcores
    },
    {
      key: 'DB Data Size',
      value: planDetails?.dbdatasize
    },
    {
      key: 'DB System Size',
      value: planDetails?.dbSystemSize
    },
    {
      key: 'DB IOPS',
      value: planDetails?.dbiops
    },
    {
      key: 'DB CPU Freq',
      value: planDetails?.dbcpufreq
    },
    {
      key: 'Proxy Core(s)',
      value: planDetails?.prxcores
    },
    {
      key: 'Proxy Data Size',
      value: planDetails?.prxdatasize
    },
    {
      key: 'Infra Cost',
      value: planDetails?.infracost
    },
    {
      key: 'License Cost',
      value: planDetails?.licencecost
    },
    {
      key: 'DBA Cost',
      value: planDetails?.dbacost
    },
    {
      key: 'Sys Cost',
      value: planDetails?.syscost
    },
    {
      key: 'Devise',
      value: planDetails?.devise
    }
  ]: [], [planDetails])

  return (
    <Modal isOpen={isOpen} size={"lg"} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{'New Cluster'}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='5'>
            <FormControl isInvalid={clusterNameError}>
              <FormLabel htmlFor='clustername'>Cluster Name</FormLabel>
              <Input id='clustername' type='text' isRequired={true} value={clusterName} onChange={(e) => setClusterName(e.target.value)} />
              <FormErrorMessage>{clusterNameError}</FormErrorMessage>
            </FormControl>
            <FormControl isInvalid={orchestratorError}>
              <FormLabel htmlFor='plan'>Orchestrator</FormLabel>
              <Dropdown
                id='orchestrator'
                isMenuPortalTarget={false}
                onChange={(option) => {
                  setOrchestrator(option.value)
                }}
                selectedValue={defaultOrchestrator}
                options={orchestratorOptions}
                className={parentStyles.fullWidth}
              />
              <FormErrorMessage>{orchestratorError}</FormErrorMessage>
            </FormControl>
            <FormControl isInvalid={planError}>
              <FormLabel htmlFor='plan'>Cluster Plan</FormLabel>
              <Dropdown
                id='plan'
                isMenuPortalTarget={false}
                onChange={(option) => {
                  onPlanChange(option)
                }}
                options={planOptions}
                className={parentStyles.fullWidth}
              />
              <FormErrorMessage>{planError}</FormErrorMessage>
            </FormControl>
            { planDetails && (<TableType2 dataArray={dataObject} className={parentStyles.table} />) }
          </Stack>
        </ModalBody>

        <ModalFooter gap={3} margin='auto'>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={closeModal}>
            No
          </RMButton>
          <RMButton onClick={handleCreateNewCluster} size='medium'>
            Yes
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default NewClusterModal
