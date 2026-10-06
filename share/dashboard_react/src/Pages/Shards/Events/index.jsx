import { Fragment, useCallback, useMemo, useState } from 'react'
import PropTypes from 'prop-types'
import { useDispatch, useSelector } from 'react-redux'
import { createColumnHelper } from '@tanstack/react-table'
import {
  Alert,
  AlertDescription,
  AlertIcon,
  AlertTitle,
  Badge,
  Box,
  Button,
  Checkbox,
  Flex,
  HStack,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalHeader,
  ModalOverlay,
  Select,
  Text,
  Tooltip,
  VStack
} from '@chakra-ui/react'
import { TbAlertCircle, TbAlertTriangle, TbCheck, TbPlayerPause, TbX } from 'react-icons/tb'
import { getDatabaseService } from '../../../redux/clusterSlice'
import { DataTable } from '../../../components/DataTable'
import modalStyles from '../../../components/Modals/styles.module.scss'
import { useTheme } from '../../../ThemeProvider'
import { EVENT_STATUS, STATUS_FILTERS, buildEventMatrix, eventOnCompletion, eventSchedule, eventSchemas, filterEventRows } from './eventsMatrix'

// Read-only Events section of the cluster Schema tab (monitoring-event-status):
// the event scheduler and the status of every event on each server of the
// cluster, from the monitored eventStatus. A definition is read on demand,
// when the user clicks an event on a server (GET .../servers/{serverName}/events).
// Nothing here changes a server.

const columnHelper = createColumnHelper()

const noteText = {
  missing: 'missing',
  'running-on-replica': 'ENABLED on a replica with the scheduler ON: it runs there',
  'enabled-on-replica': 'ENABLED on a replica: it runs if the scheduler is turned on'
}

const shortName = (name) => name.split(/[.:]/)[0]

const statusLook = (cell) => {
  if (cell.note) {
    const danger = cell.note === 'running-on-replica'
    return { icon: <TbAlertTriangle color={danger ? 'red' : 'orange'} size={16} />, color: danger ? 'red.500' : 'orange.500', bold: true }
  }
  return (
    {
      [EVENT_STATUS.ENABLED]: { icon: <TbCheck color='green' size={16} />, color: 'green.600' },
      [EVENT_STATUS.DISABLED]: { icon: <TbX color='gray' size={16} />, color: 'gray.500' },
      [EVENT_STATUS.REPLICA_SIDE_DISABLED]: { icon: <TbPlayerPause color='#3182ce' size={16} />, color: 'blue.500' }
    }[cell.status] || { icon: <TbAlertCircle color='red' size={16} />, color: 'red.500' }
  )
}

// StatusCell shows one event on one server as an icon and the status in the
// server's own words, like PFS Instruments. A present event is a button that
// opens its definition on that server.
function StatusCell({ cell, serverName, loading, onView }) {
  if (!cell) {
    return (
      <HStack spacing={1} justify='center'>
        <TbAlertCircle color='red' size={16} />
        <Text fontSize='sm' color='red.500' fontWeight='bold'>
          Missing
        </Text>
      </HStack>
    )
  }
  const look = statusLook(cell)
  return (
    <Tooltip label={`View the definition on ${serverName}`}>
      <Button size='xs' variant='ghost' leftIcon={look.icon} isLoading={loading} onClick={onView}>
        <Text fontSize='sm' color={look.color} fontWeight={look.bold ? 'bold' : 'normal'}>
          {cell.label}
        </Text>
      </Button>
    </Tooltip>
  )
}

// DefinitionModal shows an event definition as plain text: the body is SQL
// written by the database's users, so it is rendered by React (escaped), never
// as HTML.
function DefinitionModal({ definition, onClose }) {
  const { theme } = useTheme()
  const [copied, setCopied] = useState(false)
  const copy = () => {
    navigator.clipboard?.writeText(definition.text).then(() => setCopied(true), () => setCopied(false))
  }
  return (
    <Modal isOpen={true} onClose={onClose} size='4xl' scrollBehavior='inside'>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? modalStyles.modalLightContent : modalStyles.modalDarkContent}>
        <ModalHeader>
          <Text fontSize='md'>{definition.title}</Text>
        </ModalHeader>
        <ModalCloseButton />
        <ModalBody pb={6}>
          <VStack align='stretch' spacing={3}>
            {definition.event && <EventDetails event={definition.event} status={definition.status} />}
            {definition.isDefinition && (
              <Box>
                <Button size='sm' colorScheme='blue' onClick={copy}>
                  {copied ? 'Copied' : 'Copy to clipboard'}
                </Button>
              </Box>
            )}
            <Box
              as='pre'
              fontFamily='mono'
              fontSize='sm'
              whiteSpace='pre-wrap'
              wordBreak='break-word'
              p={3}
              borderWidth='1px'
              borderRadius='md'
              maxH='60vh'
              overflowY='auto'>
              {definition.text}
            </Box>
          </VStack>
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

// EventDetails lists when the event runs and what it is, as the server reports
// it; a line whose value is empty is left out.
function EventDetails({ event, status }) {
  const lines = [
    ['Status', status],
    ['Schedule', eventSchedule(event)],
    ['Last executed', event.lastExecuted || 'never'],
    ['On completion', eventOnCompletion(event.onCompletion)],
    ['Time zone', event.timeZone],
    ['Definer', event.definer],
    ['Comment', event.comment]
  ].filter(([, value]) => value)
  return (
    <Box as='dl' display='grid' gridTemplateColumns='max-content 1fr' columnGap={4} rowGap={1} fontSize='sm'>
      {lines.map(([label, value]) => (
        <Fragment key={label}>
          <Box as='dt' fontWeight='bold'>
            {label}
          </Box>
          <Box as='dd'>{value}</Box>
        </Fragment>
      ))}
    </Box>
  )
}

EventDetails.propTypes = {
  event: PropTypes.object,
  status: PropTypes.string
}

DefinitionModal.propTypes = {
  definition: PropTypes.object,
  onClose: PropTypes.func
}

StatusCell.propTypes = {
  cell: PropTypes.object,
  serverName: PropTypes.string,
  loading: PropTypes.bool,
  onView: PropTypes.func
}

function Events({ clusterName }) {
  const dispatch = useDispatch()
  const { clusterServers, clusterMaster } = useSelector((state) => state.cluster)

  const [search, setSearch] = useState('')
  const [schemaFilter, setSchemaFilter] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [showObservationsOnly, setShowObservationsOnly] = useState(false)
  const [loadingKey, setLoadingKey] = useState('')
  const [definition, setDefinition] = useState(null)

  const matrix = useMemo(() => buildEventMatrix(clusterServers, clusterMaster?.id), [clusterServers, clusterMaster?.id])

  const missingCount = matrix.rows.filter((r) => r.notes.some((n) => n.note === 'missing')).length
  const enabledOnReplicaCount = matrix.rows.filter((r) => r.notes.some((n) => n.note !== 'missing')).length
  const runningOnReplicaCount = matrix.rows.filter((r) => r.notes.some((n) => n.note === 'running-on-replica')).length
  const flaggedCount = matrix.rows.filter((r) => r.notes.length > 0).length

  const schemas = useMemo(() => eventSchemas(matrix.rows), [matrix])
  const rows = useMemo(
    () =>
      filterEventRows(matrix.rows, matrix.servers, {
        search,
        schema: schemaFilter,
        status: statusFilter,
        observationsOnly: showObservationsOnly
      }),
    [matrix, search, schemaFilter, statusFilter, showObservationsOnly]
  )

  // The definition of an event on one server, read only when asked for.
  const showDefinition = useCallback(
    async (row, server) => {
      const key = `${row.key}@${server.id}`
      setLoadingKey(key)
      let list = null
      let error = ''
      try {
        // only this event: ?schema=&name= (a server can hold many events)
        const result = await dispatch(
          getDatabaseService({ clusterName, serviceName: 'events', dbId: server.id, queryParams: { schema: row.db, name: row.name } })
        )
        if (result?.payload?.status === 200 && Array.isArray(result.payload.data)) {
          list = result.payload.data
        } else if (result?.meta?.condition) {
          error = 'another read of the definitions is running, try again'
        } else {
          // a refused read (403, 413, ...): the server's own message
          error = result?.payload?.errorMessage || result?.error?.message || 'the definition could not be read'
        }
      } catch (e) {
        error = e.message
      }
      setLoadingKey('')
      const ev = list?.find((e) => e.db === row.db && e.name === row.name)
      setDefinition({
        title: `${row.db}.${row.name} on ${server.name}`,
        event: ev || null,
        status: row.cells[server.id]?.label || '',
        isDefinition: !!ev,
        text: ev
          ? ev.definition
          : error
            ? error.includes('monitoring-event-status-max-definition-bytes')
              ? error
              : `Could not read the definition: ${error}`
            : `${row.db}.${row.name} is no longer defined on ${server.name}.`
      })
    },
    [dispatch, clusterName]
  )

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.key, {
        header: 'Event',
        id: 'event',
        cell: (info) => {
          const row = info.row.original
          return (
            <HStack spacing={2}>
              {row.notes.length > 0 && <TbAlertCircle color='orange' size={16} title='See the observations' />}
              <Badge colorScheme='blue' variant='solid' fontSize='xs' textTransform='none'>
                {row.db}
              </Badge>
              <Text fontSize='sm'>{row.name}</Text>
            </HStack>
          )
        }
      }),
      ...matrix.servers.map((server) =>
        columnHelper.accessor((row) => row.cells[server.id], {
          id: `srv-${server.id}`,
          header: () => (
            <VStack spacing={0}>
              <Text>{shortName(server.name)}</Text>
              <Text fontSize='xs' fontWeight='normal' textTransform='none'>
                {server.isMaster ? 'master' : 'replica'} · scheduler {server.scheduler ? 'ON' : 'OFF'}
              </Text>
            </VStack>
          ),
          cell: (info) => {
            const row = info.row.original
            return (
              <StatusCell
                cell={info.getValue()}
                serverName={server.name}
                loading={loadingKey === `${row.key}@${server.id}`}
                onView={() => showDefinition(row, server)}
              />
            )
          }
        })
      ),
      columnHelper.accessor((row) => row.notes, {
        header: 'Observations',
        id: 'notes',
        cell: (info) =>
          info.getValue().length === 0 ? null : (
            <VStack align='flex-start' spacing={0}>
              {info.getValue().map((n) => (
                <Text key={`${n.serverId}-${n.note}`} fontSize='sm' color={n.note === 'running-on-replica' ? 'red.500' : 'orange.500'}>
                  {shortName(n.serverName)}: {noteText[n.note] || n.note}
                </Text>
              ))}
            </VStack>
          )
      })
    ],
    [matrix, loadingKey, showDefinition]
  )

  return (
    <VStack align='stretch' spacing={3} width='100%'>
      {flaggedCount > 0 && (
        <Alert status={runningOnReplicaCount > 0 ? 'warning' : 'info'} color='gray.800'>
          <AlertIcon color={runningOnReplicaCount > 0 ? 'orange.500' : 'blue.500'} />
          <Box flex='1'>
            <AlertTitle>The servers do not hold the same events</AlertTitle>
            <AlertDescription>
              {missingCount > 0 &&
                `${missingCount} event${missingCount > 1 ? 's are' : ' is'} not present on every server; this can be intended. `}
              {enabledOnReplicaCount > 0 &&
                `${enabledOnReplicaCount} event${enabledOnReplicaCount > 1 ? 's are' : ' is'} ENABLED on a replica${runningOnReplicaCount > 0 ? `, ${runningOnReplicaCount} with the scheduler ON (it runs there)` : ''}. `}
              This section only reports what the servers hold; it changes nothing.
            </AlertDescription>
          </Box>
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
          <label htmlFor='events-status'>Status</label>
          <Select id='events-status' size='sm' width='12rem' value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}>
            {STATUS_FILTERS.map((f) => (
              <option key={f.value} value={f.value}>
                {f.label}
              </option>
            ))}
          </Select>
        </HStack>
        <Checkbox isChecked={showObservationsOnly} onChange={(e) => setShowObservationsOnly(e.target.checked)}>
          Show observations only {flaggedCount > 0 && `(${flaggedCount})`}
        </Checkbox>
        <HStack spacing={2} wrap='wrap'>
          <Text fontSize='sm' fontWeight='bold'>
            Event scheduler:
          </Text>
          {matrix.servers.map((server) => (
            <Badge key={server.id} colorScheme={server.scheduler ? 'green' : 'gray'} variant='solid' fontSize='xs'>
              {shortName(server.name)} ({server.isMaster ? 'master' : 'replica'}): {server.scheduler ? 'ON' : 'OFF'}
            </Badge>
          ))}
        </HStack>
      </Flex>
      {matrix.rows.length > 0 && (
        <Text fontSize='sm' px={2}>
          {rows.length === matrix.rows.length
            ? `${matrix.rows.length} event${matrix.rows.length > 1 ? 's' : ''}`
            : `${rows.length} of ${matrix.rows.length} events match the filters`}
        </Text>
      )}
      {matrix.rows.length === 0 ? (
        <Text fontSize='sm'>No event on any server of the cluster.</Text>
      ) : (
        <Box overflowX='auto'>
          <DataTable key='events' data={rows} columns={columns} enablePagination={true} />
        </Box>
      )}
      <Text fontSize='xs' color='gray.500' px={2}>
        Status and scheduler come from the monitoring and refresh on every tick. Click an event on a server to read its
        definition there.
      </Text>
      {definition && <DefinitionModal definition={definition} onClose={() => setDefinition(null)} />}
    </VStack>
  )
}

Events.propTypes = {
  clusterName: PropTypes.string
}

export default Events
