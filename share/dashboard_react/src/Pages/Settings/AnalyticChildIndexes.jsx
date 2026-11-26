import {
  Badge,
  Flex,
  FormControl,
  FormHelperText,
  FormLabel,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Stack,
  Text,
  Textarea
} from '@chakra-ui/react'
import React, { useCallback, useEffect, useState } from 'react'
import RMButton from '../../components/RMButton'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { clusterService } from '../../services/clusterService'

// The indexes of an analytic child's denormalize mode (CDC_CORE.md "Denormalize"): each one a
// denormalizing query, run on the source, and the denormalized table it fills on this child.
// The plugin derives the query's tables and their primary keys from the query itself.

const NAME = /^[a-z0-9][a-z0-9_-]{0,63}$/
const IDENT = /^[^`./]{1,64}$/

function IndexFormModal({ isOpen, closeModal, index, onSave }) {
  const [form, setForm] = useState(index || { name: '', slaveSchema: '', slaveTable: '', sql: '', maxKeysPerBatch: 0 })
  const [saving, setSaving] = useState(false)
  const set = (k) => (e) => setForm({ ...form, [k]: e.target.value })
  const valid = NAME.test(form.name) && IDENT.test(form.slaveSchema) && IDENT.test(form.slaveTable) && /^\s*select\b/i.test(form.sql)
  const save = async () => {
    setSaving(true)
    await onSave({ ...form, maxKeysPerBatch: parseInt(form.maxKeysPerBatch, 10) || 0 })
    setSaving(false)
  }
  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='3xl'>
      <ModalOverlay />
      <ModalContent>
        <ModalHeader>{index ? `Index ${index.name}` : 'Add an index'}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='3'>
            <FormControl isRequired>
              <FormLabel>Name</FormLabel>
              <Input value={form.name} isDisabled={!!index} onChange={set('name')} placeholder='order-lines' />
            </FormControl>
            <Flex gap='3'>
              <FormControl isRequired>
                <FormLabel>Schema on the child</FormLabel>
                <Input value={form.slaveSchema} onChange={set('slaveSchema')} placeholder='analytics' />
              </FormControl>
              <FormControl isRequired>
                <FormLabel>Denormalized table</FormLabel>
                <Input value={form.slaveTable} onChange={set('slaveTable')} placeholder='order_lines' />
              </FormControl>
            </Flex>
            <FormControl isRequired>
              <FormLabel>Denormalizing query</FormLabel>
              <Textarea value={form.sql} onChange={set('sql')} rows={10} fontFamily='mono' fontSize='sm'
                placeholder={'SELECT o.o_w_id, o.o_d_id, o.o_id, c.c_id, c.c_last, ...\nFROM tpcc.orders o\nJOIN tpcc.customer c ON ...'} />
              <FormHelperText>Run on the source. Inner joins; every table written with its schema, and the primary key columns of every table it reads returned, each under one name: they form the primary key of the denormalized table.</FormHelperText>
            </FormControl>
            <FormControl>
              <FormLabel>Max keys per batch</FormLabel>
              <Input value={form.maxKeysPerBatch} onChange={set('maxKeysPerBatch')} placeholder='0' />
              <FormHelperText>The changed keys one unpack restricts the query to; more are split, never dropped. 0: 10000.</FormHelperText>
            </FormControl>
          </Stack>
        </ModalBody>
        <ModalFooter gap='2'>
          <RMButton variant='outline' onClick={closeModal}>Cancel</RMButton>
          <RMButton colorScheme='blue' isLoading={saving} isDisabled={!valid} onClick={save}>{index ? 'Save' : 'Add'}</RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

function AnalyticChildIndexes({ clusterName, readOnly }) {
  const [list, setList] = useState({ indexes: [], ready: [], pinned: false })
  const [error, setError] = useState('')
  const [editing, setEditing] = useState(null)
  const [removeName, setRemoveName] = useState('')

  const load = useCallback(async () => {
    try {
      const { data } = await clusterService.getReplicationPluginIndexList(clusterName)
      setList(data)
      setError('')
    } catch (e) {
      setError(String(e?.response?.data || e?.message || 'Request failed'))
    }
  }, [clusterName])

  useEffect(() => {
    load()
  }, [load])

  const call = async (fn) => {
    try {
      await fn()
      setError('')
      await load()
      return true
    } catch (e) {
      setError(String(e?.response?.data || e?.message || 'Request failed'))
      return false
    }
  }
  const save = async (index) => {
    if (await call(() => clusterService.putReplicationPluginIndex(clusterName, index))) setEditing(null)
  }
  const canManage = !readOnly && !list.pinned

  return (
    <Stack spacing='2' mt='4'>
      <Flex justify='space-between' align='center'>
        <Text fontWeight='bold'>Denormalized tables</Text>
        <RMButton size='sm' isDisabled={!canManage} onClick={() => setEditing({})}>Add an index</RMButton>
      </Flex>
      <Text fontSize='sm'>Each index keeps the result of its denormalizing query, run on the parent, in a denormalized table on this child: filled once from a consistent snapshot (its init), then each change on a table the query reads recomputes the rows of the changed keys. Removing an index leaves its table on the child.</Text>
      {list.pinned && <Text fontSize='sm'>The indexes are set by a config file (replication-plugin-index).</Text>}
      {error && <Text fontSize='sm' color='red.500'>{error}</Text>}
      {(list.indexes || []).map((idx) => (
        <Flex key={idx.name} gap='3' align='center' borderWidth='1px' borderRadius='md' p='2'>
          <Text fontWeight='semibold' minW='10rem'>{idx.name}</Text>
          <Text fontFamily='mono' fontSize='sm' flex='1'>{`${idx.slaveSchema}.${idx.slaveTable}`}</Text>
          {(list.ready || []).includes(idx.name) ? <Badge colorScheme='green'>ready</Badge> : <Badge colorScheme='orange'>init pending</Badge>}
          <RMButton size='sm' variant='outline' isDisabled={!canManage} onClick={() => setEditing(idx)}>Edit</RMButton>
          <RMButton size='sm' variant='outline' isDisabled={!canManage} onClick={() => setRemoveName(idx.name)}>Remove</RMButton>
        </Flex>
      ))}
      {editing && <IndexFormModal isOpen={!!editing} closeModal={() => setEditing(null)} index={editing.name ? editing : null} onSave={save} />}
      {removeName && (
        <ConfirmModal isOpen={!!removeName} closeModal={() => setRemoveName('')}
          title={`Remove the index ${removeName}? Its denormalized table stays on the child.`}
          onConfirmClick={() => { call(() => clusterService.removeReplicationPluginIndex(clusterName, removeName)); setRemoveName('') }} />
      )}
    </Stack>
  )
}

export default AnalyticChildIndexes
