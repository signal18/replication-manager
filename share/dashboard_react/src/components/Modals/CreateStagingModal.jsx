import {
  Box,
  FormControl,
  FormLabel,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  NumberInput,
  NumberInputField,
  Select,
  Stack,
  Text
} from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import { useDispatch } from 'react-redux'
import RMButton from '../RMButton'
import { useTheme } from '../../ThemeProvider'
import parentStyles from './styles.module.scss'
import { clusterService } from '../../services/clusterService'
import { createChildCluster } from '../../redux/clusterSlice'

// Staging (#1988) or child (#1997) cluster of a cluster, kind staging or child. A
// staging cluster detaches a replica for the dev team; a child keeps replicating live,
// to test a migration (another release) under the parent's writes.
// The plan (confirm false) is asked first and
// shown: what will be created, or why it is refused (no logical backup on the parent
// first). Name, image and database count are optional: empty means the parent's.
// Changing a field drops the plan: Create needs a Check of what will be sent.
// A child can take another topology (#2012): columnstore makes a ColumnStore child, its
// tables converted at the seed, with the ColumnStore image and one database (the UM).

function CreateStagingModal({ selectedCluster, kind = 'staging', isOpen, closeModal }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const parent = selectedCluster?.name
  const [name, setName] = useState('')
  const [dbImage, setDbImage] = useState(selectedCluster?.config?.provDbDockerImg || '')
  const [dbCount, setDbCount] = useState(kind === 'staging' ? 3 : 2)
  const [topology, setTopology] = useState('')
  const changeTopology = (t) => {
    setTopology(t)
    setPlan(null)
    if (t === 'columnstore') {
      setDbImage('signal18/mariadb-columnstore:11.8')
      setDbCount(1)
    } else {
      setDbImage(selectedCluster?.config?.provDbDockerImg || '')
      setDbCount(2)
    }
  }
  const [plan, setPlan] = useState(null)
  const [refusal, setRefusal] = useState('')
  const [loading, setLoading] = useState(false)

  const askPlan = async () => {
    setLoading(true)
    setPlan(null)
    setRefusal('')
    try {
      const { data, status } = await clusterService.createChildCluster(parent, kind, { name, dbImage, dbCount: Number(dbCount) || 0, topology, confirm: false })
      if (status === 200) {
        setPlan(data)
      } else {
        setRefusal(typeof data === 'string' && data.trim() ? data.trim() : `HTTP ${status}`)
      }
    } catch (e) {
      setRefusal(e?.message || String(e))
    }
    setLoading(false)
  }

  useEffect(() => {
    if (isOpen && parent) {
      askPlan()
    }
  }, [isOpen, parent])

  const create = () => {
    dispatch(createChildCluster({ clusterName: parent, kind, name, dbImage, dbCount: Number(dbCount) || 0, topology }))
    closeModal()
  }

  return (
    <Modal isOpen={isOpen} size={'lg'} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{`New ${kind} cluster of ${parent}`}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing={3}>
            <FormControl>
              <FormLabel fontSize='sm'>Name</FormLabel>
              <Input id='staging-name' size='sm' value={name} placeholder={`${parent}-${kind}`} onChange={(e) => { setName(e.target.value); setPlan(null) }} />
            </FormControl>
            {kind === 'child' && (
              <FormControl>
                <FormLabel fontSize='sm'>Topology</FormLabel>
                <Select id='child-topology' size='sm' value={topology} onChange={(e) => changeTopology(e.target.value)}>
                  <option value=''>Same as a usual child</option>
                  <option value='columnstore'>ColumnStore (tables converted at the seed)</option>
                </Select>
              </FormControl>
            )}
            <FormControl>
              <FormLabel fontSize='sm'>Database image (empty: the parent's)</FormLabel>
              <Input id='staging-db-image' size='sm' value={dbImage} onChange={(e) => { setDbImage(e.target.value); setPlan(null) }} />
            </FormControl>
            <FormControl>
              <FormLabel fontSize='sm'>Databases</FormLabel>
              <NumberInput id='staging-db-count' size='sm' min={1} max={5} value={dbCount} onChange={(v) => { setDbCount(v); setPlan(null) }} maxW='120px'>
                <NumberInputField />
              </NumberInput>
            </FormControl>
            <Box fontSize='sm'>
              {loading && <Text>Checking…</Text>}
              {refusal && <Text color='red.500'>Refused: {refusal}</Text>}
              {plan && (
                <Stack spacing={1}>
                  <Text>
                    <b>{plan.name}</b> on {plan.orchestrator}{plan.topology ? `, topology ${plan.topology}` : ''}: {plan.dbCount} × {plan.dbImage}, {plan.dbuPerNode} DBU per node, proxy{' '}
                    {plan.proxy || 'none'}
                  </Text>
                  <Text>
                    GTID domain {plan.domain}. Its master replicates from {plan.parent} through the channel{' '}
                    {plan.sourceName ? `'${plan.sourceName}'` : 'default'} (the parent's), its own replicas through '{plan.ownSourceName}'. The
                    parent's monitoring and replication accounts and {plan.backupLogicalType} backups.
                  </Text>
                  <Text>
                    Seeded from {plan.backup}, data only: it keeps its own system tables and release.
                    {kind === 'staging' ? ' One replica is then detached as the staging server.' : ' Every node keeps replicating from the parent.'}
                  </Text>
                  {(plan.notes || []).map((n) => (
                    <Text key={n} color='orange.500'>
                      {n}
                    </Text>
                  ))}
                </Stack>
              )}
            </Box>
          </Stack>
        </ModalBody>
        <ModalFooter gap={3} margin='auto'>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={askPlan}>
            Check
          </RMButton>
          <RMButton size='medium' onClick={create} isDisabled={!plan || loading}>
            Create
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default CreateStagingModal
