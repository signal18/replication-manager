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
  Stack,
  Text
} from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import { useDispatch } from 'react-redux'
import RMButton from '../RMButton'
import { useTheme } from '../../ThemeProvider'
import parentStyles from './styles.module.scss'
import { clusterService } from '../../services/clusterService'
import { alterEngine } from '../../redux/clusterSlice'
import { sizeOf } from '../../utility/common'

// Storage engine switch (#1999): ALTER TABLE ... ENGINE on the master. With a table, that
// table only; without, every table of engine "from" (optionally of one schema): tables
// of any other engine are not touched. The plan (confirm false) is asked first; Run
// needs a Check of what is sent.

function EngineSwitchModal({ selectedCluster, schema = '', table = '', engine = '', isOpen, closeModal }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const clusterName = selectedCluster?.name
  const oneTable = !!table
  const [from, setFrom] = useState(engine || 'InnoDB')
  const [to, setTo] = useState('')
  const [onlySchema, setOnlySchema] = useState(schema)
  const [plan, setPlan] = useState(null)
  const [refusal, setRefusal] = useState('')
  const [loading, setLoading] = useState(false)

  const request = (confirm) => ({ clusterName, schema: onlySchema, table, engineFrom: from, engineTo: to, confirm })

  const check = async () => {
    setPlan(null)
    setRefusal('')
    if (!to) {
      setRefusal('Set the target engine')
      return
    }
    setLoading(true)
    try {
      const { data, status } = await clusterService.alterEngine(request(false))
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
    setPlan(null)
  }, [from, to, onlySchema])

  const run = () => {
    dispatch(alterEngine(request(true)))
    closeModal()
  }

  return (
    <Modal isOpen={isOpen} size={'xl'} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{oneTable ? `Switch the engine of ${schema}.${table}` : `Switch storage engine on ${clusterName}`}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing={3}>
            {!oneTable && (
              <>
                <FormControl>
                  <FormLabel fontSize='sm'>From engine (only tables on it change)</FormLabel>
                  <Input id='engine-from' size='sm' value={from} onChange={(e) => setFrom(e.target.value)} />
                </FormControl>
                <FormControl>
                  <FormLabel fontSize='sm'>Schema (empty: every user schema)</FormLabel>
                  <Input id='engine-schema' size='sm' value={onlySchema} onChange={(e) => setOnlySchema(e.target.value)} />
                </FormControl>
              </>
            )}
            {oneTable && <Text fontSize='sm'>Current engine: {engine}</Text>}
            <FormControl>
              <FormLabel fontSize='sm'>To engine</FormLabel>
              <Input id='engine-to' size='sm' value={to} placeholder='RocksDB, Aria, ColumnStore…' onChange={(e) => setTo(e.target.value)} />
            </FormControl>
            <Box fontSize='sm'>
              {loading && <Text>Checking…</Text>}
              {refusal && <Text color='red.500'>Refused: {refusal}</Text>}
              {plan && (
                <Stack spacing={1}>
                  <Text>
                    On {plan.server}: {(plan.tables || []).length} table(s) to switch, {sizeOf(plan.totalBytes || 0)} in total.
                  </Text>
                  {(plan.tables || []).slice(0, 20).map((t) => (
                    <Text key={`${t.schema}.${t.table}`} fontSize='xs'>
                      {t.schema}.{t.table} ({t.engine}, {sizeOf(t.bytes || 0)})
                    </Text>
                  ))}
                  {(plan.tables || []).length > 20 && <Text fontSize='xs'>… and {plan.tables.length - 20} more</Text>}
                  {(plan.skipped || []).map((t) => (
                    <Text key={`skip-${t.schema}.${t.table}`} fontSize='xs' color='orange.500'>
                      skipped {t.schema}.{t.table}: {t.reason}
                    </Text>
                  ))}
                  {(plan.notes || []).map((n) => (
                    <Text key={n} fontSize='xs' color='orange.500'>
                      {n}
                    </Text>
                  ))}
                </Stack>
              )}
            </Box>
          </Stack>
        </ModalBody>
        <ModalFooter gap={3} margin='auto'>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={check}>
            Check
          </RMButton>
          <RMButton size='medium' onClick={run} isDisabled={!plan || loading || !(plan.tables || []).length}>
            Run
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default EngineSwitchModal
