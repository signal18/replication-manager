import { useEffect, useMemo, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { Box, Button, Flex, HStack, Spinner, Table, Tbody, Td, Text, Th, Thead, Tr, VStack } from '@chakra-ui/react'
import { fetchBillingSubscription, fetchBillingTransactions, fetchPersonalBalance } from '../../redux/billingSlice'

const PAGE_SIZE = 20

function Billing() {
  const dispatch = useDispatch()
  const [offset, setOffset] = useState(0)

  const {
    balance,
    subscription,
    transactions,
    loadingBalance,
    loadingSubscription,
    loadingTransactions,
    errorBalance,
    errorSubscription,
    errorTransactions
  } = useSelector((state) => state.billing)

  const rows = useMemo(() => (Array.isArray(transactions) ? transactions : []), [transactions])

  useEffect(() => {
    dispatch(fetchPersonalBalance())
    dispatch(fetchBillingSubscription())
  }, [dispatch])

  useEffect(() => {
    dispatch(fetchBillingTransactions({ limit: PAGE_SIZE, offset, direction: 'desc' }))
  }, [dispatch, offset])

  const balanceValue = balance?.balance ?? balance?.credits ?? balance?.amount ?? '—'
  const balanceCurrency = balance?.currency || ''
  const planLabel = subscription?.plan || subscription?.status || '—'
  const pendingLabel = subscription?.pending_request || subscription?.pending || null

  return (
    <VStack align='stretch' spacing={4}>
      <HStack align='stretch' spacing={4} flexWrap='wrap'>
        <Box borderWidth='1px' borderRadius='md' p={4} minW='300px' flex='1'>
          <Text fontSize='sm' color='gray.500'>Personal Balance</Text>
          {loadingBalance ? (
            <HStack py={3}><Spinner size='sm' /><Text fontSize='sm'>Loading balance…</Text></HStack>
          ) : errorBalance ? (
            <Text fontSize='sm' color='red.500'>{errorBalance}</Text>
          ) : (
            <Text fontSize='2xl' fontWeight='bold'>{balanceValue} {balanceCurrency}</Text>
          )}
        </Box>

        <Box borderWidth='1px' borderRadius='md' p={4} minW='300px' flex='1'>
          <Text fontSize='sm' color='gray.500'>DBaaS Subscription</Text>
          {loadingSubscription ? (
            <HStack py={3}><Spinner size='sm' /><Text fontSize='sm'>Loading subscription…</Text></HStack>
          ) : errorSubscription ? (
            <Text fontSize='sm' color='red.500'>{errorSubscription}</Text>
          ) : (
            <VStack align='start' spacing={1}>
              <Text fontSize='lg' fontWeight='semibold'>{planLabel}</Text>
              {pendingLabel && <Text fontSize='sm' color='orange.500'>Pending: {String(pendingLabel)}</Text>}
            </VStack>
          )}
        </Box>
      </HStack>

      <Box borderWidth='1px' borderRadius='md' p={4}>
        <Flex justify='space-between' align='center' mb={3}>
          <Text fontSize='md' fontWeight='semibold'>Transactions</Text>
          <HStack>
            <Button size='sm' onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))} isDisabled={offset === 0 || loadingTransactions}>
              Previous
            </Button>
            <Button size='sm' onClick={() => setOffset(offset + PAGE_SIZE)} isDisabled={loadingTransactions || rows.length < PAGE_SIZE}>
              Next
            </Button>
          </HStack>
        </Flex>

        {loadingTransactions ? (
          <HStack py={3}><Spinner size='sm' /><Text fontSize='sm'>Loading transactions…</Text></HStack>
        ) : errorTransactions ? (
          <Text fontSize='sm' color='red.500'>{errorTransactions}</Text>
        ) : rows.length === 0 ? (
          <Text fontSize='sm' color='gray.500'>No transactions found.</Text>
        ) : (
          <Table size='sm'>
            <Thead>
              <Tr>
                <Th>Date</Th>
                <Th>Description</Th>
                <Th isNumeric>Amount</Th>
                <Th>Status</Th>
              </Tr>
            </Thead>
            <Tbody>
              {rows.map((row, idx) => {
                const key = row?.id || row?.transaction_id || row?.tx_id || `${offset}-${idx}`
                return (
                  <Tr key={key}>
                    <Td>{String(row?.created_at || row?.date || row?.timestamp || '—')}</Td>
                    <Td>{String(row?.description || row?.label || row?.type || '—')}</Td>
                    <Td isNumeric>{String(row?.amount ?? row?.credits ?? '—')}</Td>
                    <Td>{String(row?.status || '—')}</Td>
                  </Tr>
                )
              })}
            </Tbody>
          </Table>
        )}
      </Box>
    </VStack>
  )
}

export default Billing
