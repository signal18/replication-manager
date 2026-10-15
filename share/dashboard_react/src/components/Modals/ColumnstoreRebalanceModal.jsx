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
import { rebalanceColumnstore } from '../../redux/clusterSlice'
import { sizeOf } from '../../utility/common'

// ColumnStore rebalance (#2008): rebuild the ColumnStore tables whose extents are not on
// every dbroot (a PM added since they were written). With a table, that table only;
// without, every such table (optionally of one schema). The plan (confirm false) is asked
// first; Run needs a Check of what is sent.

const extentsText = (extents, dbroots) =>
  (dbroots || []).map((d) => `dbroot ${d}: ${(extents || {})[d] || 0}`).join(', ')

function ColumnstoreRebalanceModal({ selectedCluster, schema = '', table = '', isOpen, closeModal }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const clusterName = selectedCluster?.name
  const oneTable = !!table
  const [onlySchema, setOnlySchema] = useState(schema)
  const [plan, setPlan] = useState(null)
  const [refusal, setRefusal] = useState('')
  const [loading, setLoading] = useState(false)

  const request = (confirm) => ({ clusterName, schema: onlySchema, table, confirm })

  const check = async () => {
    setPlan(null)
    setRefusal('')
    setLoading(true)
    try {
      const { data, status } = await clusterService.rebalanceColumnstore(request(false))
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
  }, [onlySchema])

  const run = () => {
    dispatch(rebalanceColumnstore(request(true)))
    closeModal()
  }

  return (
    <Modal isOpen={isOpen} size={'xl'} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{oneTable ? `Rebalance ${schema}.${table} over every dbroot` : `Rebalance ColumnStore tables on ${clusterName}`}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing={3}>
            {!oneTable && (
              <FormControl>
                <FormLabel fontSize='sm'>Schema (empty: every user schema)</FormLabel>
                <Input id='rebalance-schema' size='sm' value={onlySchema} onChange={(e) => setOnlySchema(e.target.value)} />
              </FormControl>
            )}
            <Text fontSize='sm'>
              Each table is copied whole, then swapped in one rename: its writes wait during the copy, its reads go on.
            </Text>
            <Box fontSize='sm'>
              {loading && <Text>Checking…</Text>}
              {refusal && <Text color='red.500'>Refused: {refusal}</Text>}
              {plan && (
                <Stack spacing={1}>
                  <Text>
                    On {plan.server}, dbroots {(plan.dbroots || []).join(', ')}: {(plan.tables || []).length} table(s) to rebalance, {sizeOf(plan.totalBytes || 0)} in total.
                  </Text>
                  {(plan.tables || []).slice(0, 20).map((t) => (
                    <Text key={`${t.schema}.${t.table}`} fontSize='xs'>
                      {t.schema}.{t.table} ({sizeOf(t.bytes || 0)}; extents {extentsText(t.extents, plan.dbroots)})
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

export default ColumnstoreRebalanceModal
