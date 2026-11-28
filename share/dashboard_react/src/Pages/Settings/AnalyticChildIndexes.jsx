import {
  Badge,
  Flex,
  FormControl,
  FormHelperText,
  FormLabel,
  Input,
  Select,
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

// The indexes of an analytic child (CDC_CORE.md "Modes"): an index of a denormalizing query,
// run on the source, and the denormalized table it fills on this child (the plugin derives the
// query's tables and their primary keys from the query itself), or an index of tables copied as
// they are (table mode). Either one partitioned on the child when its destination needs it.

const NAME = /^[a-z0-9][a-z0-9_-]{0,63}$/
const IDENT = /^[^`./]{1,64}$/
const TABLE = /^[^`./\s]{1,64}\.[^`./\s]{1,64}$/

function IndexFormModal({ isOpen, closeModal, index, onSave }) {
  const initial = index ? { ...index, tables: (index.tables || []).join(', ') } : null
  const [form, setForm] = useState(initial || { name: '', mode: 'index', tables: '', init: '', slaveSchema: '', slaveTable: '', sql: '', maxKeysPerBatch: 0, rangeStep: 0, partitionColumn: '', partitionPeriod: '', partitionsAhead: 0, targetEngine: '' })
  const [saving, setSaving] = useState(false)
  const set = (k) => (e) => setForm({ ...form, [k]: e.target.value })
  const tableMode = form.mode === 'table'
  const tables = String(form.tables || '').split(',').map((t) => t.trim()).filter(Boolean)
  const partitioned = !!form.partitionColumn || !!form.partitionPeriod
  const valid =
    NAME.test(form.name) &&
    IDENT.test(form.slaveSchema) &&
    (tableMode
      ? tables.length > 0 && tables.every((t) => TABLE.test(t)) && (!form.slaveTable || (IDENT.test(form.slaveTable) && tables.length === 1))
      : IDENT.test(form.slaveTable) && /^\s*select\b/i.test(form.sql)) &&
    (!partitioned || (IDENT.test(form.partitionColumn) && !!form.partitionPeriod))
  const save = async () => {
    setSaving(true)
    const int = (v) => parseInt(v, 10) || 0
    await onSave({
      ...form,
      mode: tableMode ? 'table' : '',
      tables: tableMode ? tables : [],
      sql: tableMode ? '' : form.sql,
      init: tableMode ? form.init : '',
      maxKeysPerBatch: int(form.maxKeysPerBatch),
      rangeStep: int(form.rangeStep),
      partitionsAhead: int(form.partitionsAhead)
    })
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
            <Flex gap='3'>
              <FormControl isRequired>
                <FormLabel>Name</FormLabel>
                <Input value={form.name} isDisabled={!!index} onChange={set('name')} placeholder='order-lines' />
              </FormControl>
              <FormControl>
                <FormLabel>Mode</FormLabel>
                <Select value={tableMode ? 'table' : 'index'} onChange={set('mode')}>
                  <option value='index'>index: a denormalizing query</option>
                  <option value='table'>table: tables copied as they are</option>
                </Select>
              </FormControl>
            </Flex>
            <Flex gap='3'>
              <FormControl isRequired>
                <FormLabel>Schema on the child</FormLabel>
                <Input value={form.slaveSchema} onChange={set('slaveSchema')} placeholder={tableMode ? 'logs_partitioned' : 'analytics'} />
              </FormControl>
              <FormControl isRequired={!tableMode}>
                <FormLabel>{tableMode ? 'Table name on the child (one table only)' : 'Denormalized table'}</FormLabel>
                <Input value={form.slaveTable} onChange={set('slaveTable')} placeholder={tableMode ? 'the source name' : 'order_lines'} />
              </FormControl>
            </Flex>
            {tableMode ? (
              <Flex gap='3'>
                <FormControl isRequired>
                  <FormLabel>Tables</FormLabel>
                  <Input value={form.tables} onChange={set('tables')} placeholder='logs.access, logs.audit' fontFamily='mono' />
                  <FormHelperText>schema.table, comma separated: each one copied with its primary key into the schema on the child.</FormHelperText>
                </FormControl>
                <FormControl>
                  <FormLabel>Init</FormLabel>
                  <Select value={form.init || 'select'} onChange={set('init')}>
                    <option value='select'>select: a consistent snapshot, range by range</option>
                    <option value='backup'>backup: the parent's latest split dump</option>
                  </Select>
                  <FormHelperText>The backup puts no load on the parent; it must not be older than the feed.</FormHelperText>
                </FormControl>
              </Flex>
            ) : (
              <FormControl isRequired>
                <FormLabel>Denormalizing query</FormLabel>
                <Textarea value={form.sql} onChange={set('sql')} rows={10} fontFamily='mono' fontSize='sm'
                  placeholder={'SELECT o.o_w_id, o.o_d_id, o.o_id, c.c_id, c.c_last, ...\nFROM tpcc.orders o\nJOIN tpcc.customer c ON ...'} />
                <FormHelperText>Run on the source. Inner joins; every table written with its schema, and the primary key columns of every table it reads returned, each under one name: they form the primary key of the denormalized table.</FormHelperText>
              </FormControl>
            )}
            <Flex gap='3'>
              <FormControl>
                <FormLabel>Partition column</FormLabel>
                <Input value={form.partitionColumn} onChange={set('partitionColumn')} placeholder='created_at' />
                <FormHelperText>A DATE, DATETIME or TIMESTAMP column, when the destination is partitioned.</FormHelperText>
              </FormControl>
              <FormControl>
                <FormLabel>Partition period</FormLabel>
                <Select value={form.partitionPeriod} onChange={set('partitionPeriod')}>
                  <option value=''>none</option>
                  <option value='day'>day</option>
                  <option value='week'>week</option>
                  <option value='month'>month</option>
                </Select>
              </FormControl>
              <FormControl>
                <FormLabel>Partitions ahead</FormLabel>
                <Input value={form.partitionsAhead} onChange={set('partitionsAhead')} placeholder='0' />
                <FormHelperText>Made before they are needed. 0: 3.</FormHelperText>
              </FormControl>
            </Flex>
            <Flex gap='3'>
              <FormControl>
                <FormLabel>Target engine</FormLabel>
                <Input value={form.targetEngine} onChange={set('targetEngine')} placeholder='InnoDB' />
              </FormControl>
              {tableMode ? (
                <FormControl>
                  <FormLabel>Range step</FormLabel>
                  <Input value={form.rangeStep} onChange={set('rangeStep')} placeholder='0' />
                  <FormHelperText>Rows per range of a select init. 0: 10000.</FormHelperText>
                </FormControl>
              ) : (
                <FormControl>
                  <FormLabel>Max keys per batch</FormLabel>
                  <Input value={form.maxKeysPerBatch} onChange={set('maxKeysPerBatch')} placeholder='0' />
                  <FormHelperText>The changed keys one unpack restricts the query to; more are split, never dropped. 0: 10000.</FormHelperText>
                </FormControl>
              )}
            </Flex>
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
        <Text fontWeight='bold'>Indexes</Text>
        <RMButton size='sm' isDisabled={!canManage} onClick={() => setEditing({})}>Add an index</RMButton>
      </Flex>
      <Text fontSize='sm'>An index of a query keeps its result, run on the parent, in a denormalized table on this child; an index of tables copies them as they are into its schema on this child. Each one is filled once (its init: a consistent snapshot, or for a table the parent's latest split dump), then kept up to date from the parent's binlog, partitioned by its period when it has a partition column. Removing an index leaves its tables on the child.</Text>
      {list.pinned && <Text fontSize='sm'>The indexes are set by a config file (replication-plugin-index).</Text>}
      {error && <Text fontSize='sm' color='red.500'>{error}</Text>}
      {(list.indexes || []).map((idx) => (
        <Flex key={idx.name} gap='3' align='center' borderWidth='1px' borderRadius='md' p='2'>
          <Text fontWeight='semibold' minW='10rem'>{idx.name}</Text>
          <Text fontFamily='mono' fontSize='sm' flex='1'>
            {idx.mode === 'table' ? `${(idx.tables || []).join(', ')} → ${idx.slaveSchema}${idx.slaveTable ? '.' + idx.slaveTable : ''}` : `${idx.slaveSchema}.${idx.slaveTable}`}
            {idx.partitionColumn ? ` (by ${idx.partitionPeriod} of ${idx.partitionColumn})` : ''}
          </Text>
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
