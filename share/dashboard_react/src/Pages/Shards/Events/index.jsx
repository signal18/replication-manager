import { useCallback, useEffect, useMemo, useState } from 'react'
import PropTypes from 'prop-types'
import { useDispatch } from 'react-redux'
import { createColumnHelper } from '@tanstack/react-table'
import { Alert, AlertDescription, AlertIcon, AlertTitle, Badge, Box, Button, Flex, HStack, Input, Select, Text, Tooltip, VStack } from '@chakra-ui/react'
import { TbAlertTriangle, TbCheck, TbHelp, TbX } from 'react-icons/tb'
import { getSchemaEvents } from '../../../redux/clusterSlice'
import { DataTable } from '../../../components/DataTable'
import { COLLECTION_LABELS, DRIFT_LABELS, DRIFT_ORDER, buildEventRows, driftKinds, eventSchemas, filterEventRows } from './eventsMatrix'

// Scheduled Database Events section of the cluster Schema tab
// (monitoring-schema-events): whether the MySQL/MariaDB EVENT objects of the
// replicas match the master's, from GET .../schema/events. repman compares;
// this page only shows the result. It never shows an event definition.

const columnHelper = createColumnHelper()

const shortName = (name) => (name || '').split(/[.:]/)[0]

const formatTime = (unix) => (unix ? new Date(unix * 1000).toLocaleString() : '')

// EventCell shows one event on one server: present (status class and short
// definition CRC), absent, or not checked, highlighted when repman reported a
// drift there.
function EventCell({ cell }) {
  if (!cell || cell.state === 'not-checked') {
    return (
      <HStack spacing={1} justify='center'>
        <TbHelp color='gray' size={16} />
        <Text fontSize='sm' color='gray.500'>
          not checked
        </Text>
      </HStack>
    )
  }
  const drifted = cell.drifts.length > 0
  const tip = drifted ? cell.drifts.map((d) => DRIFT_LABELS[d] || d).join(', ') : ''
  const body =
    cell.state === 'absent' ? (
      <HStack spacing={1} justify='center'>
        {drifted ? <TbAlertTriangle color='orange' size={16} /> : <TbX color='gray' size={16} />}
        <Text fontSize='sm' color={drifted ? 'orange.500' : 'gray.500'} fontWeight={drifted ? 'bold' : 'normal'}>
          absent
        </Text>
      </HStack>
    ) : (
      <HStack spacing={1} justify='center'>
        {drifted ? <TbAlertTriangle color='orange' size={16} /> : <TbCheck color='green' size={16} />}
        <Text fontSize='sm' color={drifted ? 'orange.500' : 'green.600'} fontWeight={drifted ? 'bold' : 'normal'}>
          {cell.status}
        </Text>
        <Text fontSize='xs' fontFamily='mono' color='gray.500'>
          {cell.crc}
        </Text>
      </HStack>
    )
  return tip ? <Tooltip label={tip}>{body}</Tooltip> : body
}

EventCell.propTypes = {
  cell: PropTypes.object
}

function Events({ clusterName }) {
  const dispatch = useDispatch()
  const [view, setView] = useState(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [search, setSearch] = useState('')
  const [schemaFilter, setSchemaFilter] = useState('')
  const [driftFilter, setDriftFilter] = useState('')

  const load = useCallback(async () => {
    if (!clusterName) return
    setLoading(true)
    try {
      const result = await dispatch(getSchemaEvents({ clusterName }))
      if (result?.payload?.status === 200) {
        setView(result.payload.data)
        setError('')
      } else if (!result?.meta?.condition) {
        setError(result?.payload?.errorMessage || result?.error?.message || 'could not read the scheduled database events')
      }
    } catch (e) {
      setError(e.message)
    }
    setLoading(false)
  }, [dispatch, clusterName])

  useEffect(() => {
    load()
  }, [load])

  const { servers, rows: allRows } = useMemo(() => buildEventRows(view), [view])
  const schemas = useMemo(() => eventSchemas(allRows), [allRows])
  const rows = useMemo(() => filterEventRows(allRows, { search, schema: schemaFilter, drift: driftFilter }), [allRows, search, schemaFilter, driftFilter])
  const driftedRows = allRows.filter((r) => r.drifts.length > 0).length
  const differentReplicas = servers.filter((s) => s.comparison === 'different')
  const uncheckedServers = servers.filter((s) => s.collection !== 'checked')

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.key, {
        header: 'Event',
        id: 'event',
        cell: (info) => {
          const row = info.row.original
          return (
            <HStack spacing={2}>
              <Badge colorScheme='blue' variant='solid' fontSize='xs' textTransform='none'>
                {row.db}
              </Badge>
              <Text fontSize='sm'>{row.name}</Text>
            </HStack>
          )
        }
      }),
      ...servers.map((srv) =>
        columnHelper.accessor((row) => row.cells[srv.id], {
          id: `srv-${srv.id}`,
          header: () => (
            <VStack spacing={0}>
              <Text>{shortName(srv.url)}</Text>
              <Text fontSize='xs' fontWeight='normal' textTransform='none'>
                {srv.isMaster ? 'master' : 'replica'}
                {srv.collection !== 'checked' && ` · ${COLLECTION_LABELS[srv.collection] || srv.collection}`}
              </Text>
            </VStack>
          ),
          cell: (info) => <EventCell cell={info.getValue()} />
        })
      ),
      columnHelper.accessor((row) => driftKinds(row), {
        header: 'Drift',
        id: 'drift',
        cell: (info) => (
          <HStack spacing={1} wrap='wrap'>
            {info.getValue().map((k) => (
              <Tooltip key={k} label={DRIFT_LABELS[k]}>
                <Badge colorScheme='orange' variant='solid' fontSize='xs' textTransform='none'>
                  {k}
                </Badge>
              </Tooltip>
            ))}
          </HStack>
        )
      })
    ],
    [servers]
  )

  if (view && !view.enabled) {
    return <Text fontSize='sm'>Scheduled database event comparison is off for this cluster (monitoring-schema-events).</Text>
  }

  return (
    <VStack align='stretch' spacing={3} width='100%'>
      {error && (
        <Alert status='error' color='gray.800'>
          <AlertIcon />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {differentReplicas.length > 0 && (
        <Alert status='warning' color='gray.800'>
          <AlertIcon color='orange.500' />
          <Box flex='1'>
            <AlertTitle>Scheduled database events differ from the master</AlertTitle>
            <AlertDescription>
              {`${driftedRows} event${driftedRows > 1 ? 's' : ''} on ${differentReplicas.map((s) => shortName(s.url)).join(', ')}. `}
              The same drift is reported in the schema warnings (WARN0164).
            </AlertDescription>
          </Box>
        </Alert>
      )}
      {uncheckedServers.length > 0 && (
        <Alert status='info' color='gray.800'>
          <AlertIcon color='blue.500' />
          <AlertDescription>
            {uncheckedServers.map((s) => `${shortName(s.url)}: ${COLLECTION_LABELS[s.collection] || s.collection}`).join('; ')}. A server that is not
            checked is not compared: its events are unknown, not missing.
          </AlertDescription>
        </Alert>
      )}
      <Flex gap={4} wrap='wrap' align='center' px={2}>
        <HStack>
          <label htmlFor='events-search'>Search</label>
          <Input
            id='events-search'
            size='sm'
            width='16rem'
            type='search'
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder='Filter schema or event...'
          />
        </HStack>
        <HStack>
          <label htmlFor='events-schema'>Schema</label>
          <Select id='events-schema' size='sm' width='12rem' value={schemaFilter} onChange={(e) => setSchemaFilter(e.target.value)}>
            <option value=''>All schemas ({schemas.length})</option>
            {schemas.map((db) => (
              <option key={db} value={db}>
                {db}
              </option>
            ))}
          </Select>
        </HStack>
        <HStack>
          <label htmlFor='events-drift'>Drift</label>
          <Select id='events-drift' size='sm' width='14rem' value={driftFilter} onChange={(e) => setDriftFilter(e.target.value)}>
            <option value=''>All events</option>
            <option value='any'>Any drift ({driftedRows})</option>
            {DRIFT_ORDER.map((k) => (
              <option key={k} value={k}>
                {DRIFT_LABELS[k]}
              </option>
            ))}
          </Select>
        </HStack>
        <Button size='sm' onClick={load} isLoading={loading}>
          Refresh
        </Button>
      </Flex>
      {allRows.length > 0 && (
        <Text fontSize='sm' px={2}>
          {rows.length === allRows.length ? `${allRows.length} event${allRows.length > 1 ? 's' : ''}` : `${rows.length} of ${allRows.length} events match the filters`}
        </Text>
      )}
      {allRows.length === 0 ? (
        <Text fontSize='sm'>{view ? 'No scheduled database event on any checked server.' : ''}</Text>
      ) : (
        <Box overflowX='auto'>
          <DataTable key='events' data={rows} columns={columns} enablePagination={true} />
        </Box>
      )}
      <Text fontSize='xs' color='gray.500' px={2}>
        Collected by the schema scan
        {servers.find((s) => s.isMaster)?.collectedAt ? ` (master: ${formatTime(servers.find((s) => s.isMaster).collectedAt)})` : ''}. Each event shows its
        status (active: enabled, or disabled on a replica because it is replicated; disabled) and the start of the CRC64 of its definition; the
        definition itself is never shown.
      </Text>
    </VStack>
  )
}

Events.propTypes = {
  clusterName: PropTypes.string
}

export default Events
