import { useState } from 'react'
import { useSelector } from 'react-redux'
import { Box, Flex, Text, Divider, Spinner, Button } from '@chakra-ui/react'
import Card from '../../components/Card'
import Gauge from '../../components/Gauge'
import TagPill from '../../components/TagPill'
import styles from './styles.module.scss'
import Logs from '../Dashboard/components/Logs'
import AccordionComponent from '../../components/AccordionComponent'
import AlertModal from '../../components/Modals/AlertModal'

// ─── helpers ────────────────────────────────────────────────────────────────

function bytesToGB(b) {
  return b ? b / 1073741824 : 0
}

function bytesToMB(b) {
  return b ? b / 1048576 : 0
}

function fmtGB(b) {
  const v = bytesToGB(b)
  return v < 1 ? `${(v * 1024).toFixed(0)} MB` : `${v.toFixed(2)} GB`
}

function fmtMB(b) {
  const v = bytesToMB(b)
  return v < 1024 ? `${v.toFixed(0)} MB` : `${(v / 1024).toFixed(2)} GB`
}

function fmtUptime(s) {
  if (!s && s !== 0) return 'N/A'
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  if (d > 0) return `${d}d ${h}h ${m}m`
  if (h > 0) return `${h}h ${m}m ${sec}s`
  if (m > 0) return `${m}m ${sec}s`
  return `${sec}s`
}

function pct(used, total) {
  if (!total) return 0
  return Math.min((used / total) * 100, 100)
}

function usageColor(percent) {
  if (percent < 70) return 'green'
  if (percent < 90) return 'orange'
  return 'red'
}

// ─── sub-components ─────────────────────────────────────────────────────────

function ProgressRow({ label, value, total, valueLabel, totalLabel }) {
  const percent = pct(value, total)
  const color = usageColor(percent)
  return (
    <Flex direction='column' gap='4px'>
      <Flex justify='space-between' align='center'>
        <Text fontSize='sm'>{label}</Text>
        <Text fontSize='sm' fontWeight='semibold'>
          {valueLabel} / {totalLabel} ({percent.toFixed(1)}%)
        </Text>
      </Flex>
      <Box w='100%' h='8px' bg='gray.200' borderRadius='4px' overflow='hidden'>
        <Box
          h='100%'
          bg={color}
          w={`${percent}%`}
          transition='width 0.3s ease-in-out'
          borderRadius='4px'
        />
      </Box>
    </Flex>
  )
}

function MetaRow({ label, value }) {
  return (
    <Flex justify='space-between' align='center' py='2px'>
      <Text fontSize='xs' opacity={0.65}>{label}</Text>
      <Text fontSize='xs' fontWeight='medium' maxW='60%' textAlign='right' wordBreak='break-all'>
        {value ?? 'N/A'}
      </Text>
    </Flex>
  )
}

function HostCard({ host, isDesktop }) {
  const cpuPct = host?.cpuPercent ?? 0
  const memPct = host?.memoryUsedPercent ?? 0
  const diskPct = host?.diskUsedPercent ?? 0

  return (
    <Card
      width={isDesktop ? '48%' : '100%'}
      header={
        <>
          <Text>Host: {host?.hostname ?? '—'}</Text>
          <Box ml='auto'>
            <TagPill colorScheme={usageColor(Math.max(cpuPct, memPct, diskPct))} text={`${host?.cpuCores ?? '?'} cores`} />
          </Box>
        </>
      }
      body={
        <Flex direction='column' gap='10px' p='8px'>
          {/* Progress bars */}
          <Flex direction='column' gap='10px'>
            <ProgressRow
              label='CPU'
              value={cpuPct}
              total={100}
              valueLabel={`${cpuPct.toFixed(1)}%`}
              totalLabel='100%'
            />
            <ProgressRow
              label='Memory'
              value={host?.memoryUsedBytes ?? 0}
              total={host?.memoryTotalBytes ?? 1}
              valueLabel={fmtGB(host?.memoryUsedBytes)}
              totalLabel={fmtGB(host?.memoryTotalBytes)}
            />
            <ProgressRow
              label='Disk'
              value={host?.diskUsedBytes ?? 0}
              total={host?.diskTotalBytes ?? 1}
              valueLabel={fmtGB(host?.diskUsedBytes)}
              totalLabel={fmtGB(host?.diskTotalBytes)}
            />
          </Flex>

          <Divider />

          {/* Gauges */}
          <Flex wrap='wrap' justify='space-evenly' gap='0'>
            <Gauge minValue={0} maxValue={100} value={cpuPct} text={'CPU %'} width={110} height={70} />
            <Gauge
              minValue={0}
              maxValue={Math.max(bytesToGB(host?.memoryTotalBytes), 1)}
              value={bytesToGB(host?.memoryUsedBytes ?? 0)}
              text={'Mem GB'}
              width={110}
              height={70}
            />
            <Gauge
              minValue={0}
              maxValue={Math.max(bytesToGB(host?.diskTotalBytes), 1)}
              value={bytesToGB(host?.diskUsedBytes ?? 0)}
              text={'Disk GB'}
              width={110}
              height={70}
            />
          </Flex>

          <Divider />

          {/* Metadata */}
          {host?.diskError && (
            <Text fontSize='xs' color='red.400'>Disk error: {host.diskError}</Text>
          )}
          <MetaRow label='Disk path' value={host?.diskPath} />
        </Flex>
      }
    />
  )
}

function ProcessCard({ proc, isDesktop }) {
  const heapMB = bytesToMB(proc?.heapAllocBytes ?? 0)
  const rssMB = bytesToMB(proc?.rssBytes ?? 0)
  const goroutines = proc?.goroutines ?? 0

  return (
    <Card
      width={isDesktop ? '48%' : '100%'}
      header={
        <>
          <Text>Repman Process</Text>
          <Box ml='auto' display='flex' gap='4px'>
            <TagPill colorScheme='blue' text={`PID ${proc?.pid ?? '?'}`} />
            <TagPill colorScheme='purple' text={fmtUptime(proc?.uptimeSeconds)} />
          </Box>
        </>
      }
      body={
        <Flex direction='column' gap='10px' p='8px'>
          {/* Gauges */}
          <Flex wrap='wrap' justify='space-evenly' gap='0'>
            <Gauge
              minValue={0}
              maxValue={1000}
              value={goroutines}
              text={'Goroutines'}
              width={110}
              height={70}
            />
            <Gauge
              minValue={0}
              maxValue={Math.max(rssMB * 1.5, 512)}
              value={rssMB}
              text={'RSS MB'}
              width={110}
              height={70}
            />
            <Gauge
              minValue={0}
              maxValue={Math.max(heapMB * 1.5, 256)}
              value={heapMB}
              text={'Heap MB'}
              width={110}
              height={70}
            />
          </Flex>

          <Divider />

          {/* Metadata */}
          <MetaRow label='PID' value={proc?.pid} />
          <MetaRow label='Uptime' value={fmtUptime(proc?.uptimeSeconds)} />
          <MetaRow label='Goroutines' value={goroutines} />
          <MetaRow label='RSS' value={fmtMB(proc?.rssBytes)} />
          <MetaRow label='Heap alloc' value={fmtMB(proc?.heapAllocBytes)} />
          <MetaRow label='Heap sys' value={fmtMB(proc?.heapSysBytes)} />
        </Flex>
      }
    />
  )
}

// ─── global logs ─────────────────────────────────────────────────────────────

export function GlobalLogs() {
  const logs = useSelector((state) => state.globalClusters.globalLogs?.general)
  return (
    <Box className={styles.logContainer}>
      <Logs logs={logs?.buffer} searchable={true} isScrollable={false} />
    </Box>
  )
}

function AlertSection({ title, items, colorScheme, onOpenModal }) {
  const previewItems = items.slice(0, 3)
  const remainingCount = Math.max(items.length - previewItems.length, 0)

  return (
    <AccordionComponent
      heading={
        <Flex align='center' gap='8px'>
          <Text>{title}</Text>
          <TagPill colorScheme={colorScheme} text={`${items.length}`} />
        </Flex>
      }
      body={
        <Flex direction='column' gap='8px' p='8px'>
          {items.length === 0 ? (
            <Text fontSize='sm' opacity={0.7}>No active {title.toLowerCase()}.</Text>
          ) : (
            <>
              <Text fontSize='xs' opacity={0.7}>Showing latest {previewItems.length} of {items.length}.</Text>
              {previewItems.map((item, index) => (
                <Box key={`${item?.number ?? 'alert'}-${index}`} p='8px' borderRadius='6px' bg='gray.50'>
                  <Text fontSize='xs' opacity={0.7}>{item?.number ?? 'N/A'} • {item?.from ?? 'N/A'}</Text>
                  <Text fontSize='sm'>{item?.desc ?? 'N/A'}</Text>
                </Box>
              ))}
              {remainingCount > 0 && (
                <Text fontSize='xs' opacity={0.7}>
                  +{remainingCount} more {title.toLowerCase()} in navbar alert modal.
                </Text>
              )}
              <Box pt='4px'>
                <Button
                  size='xs'
                  variant='outline'
                  colorScheme={colorScheme}
                  onClick={() => onOpenModal(title === 'Errors' ? 'error' : title === 'Infos' ? 'info' : 'warning')}>
                  Open in modal
                </Button>
              </Box>
            </>
          )}
        </Flex>
      }
    />
  )
}

function GlobalAlertsBody({ globalAlerts, onOpenModal }) {
  const warnings = globalAlerts?.warnings ?? []
  const errors = globalAlerts?.errors ?? []
  const infos = globalAlerts?.infos ?? []

  return (
    <Flex direction='column' gap='12px' p='12px'>
      <AlertSection title='Errors' items={errors} colorScheme='red' onOpenModal={onOpenModal} />
      <AlertSection title='Warnings' items={warnings} colorScheme='orange' onOpenModal={onOpenModal} />
      <AlertSection title='Infos' items={infos} colorScheme='blue' onOpenModal={onOpenModal} />
    </Flex>
  )
}

// ─── metrics body ────────────────────────────────────────────────────────────

function MetricsBody({ globalMetrics, isDesktop }) {
  if (!globalMetrics) {
    return (
      <Flex align='center' gap={2} p={4}>
        <Spinner size='sm' />
        <Text fontSize='sm'>Loading metrics…</Text>
      </Flex>
    )
  }
  return (
    <Flex wrap='wrap' gap='12px' p='12px'>
      <HostCard host={globalMetrics.host} isDesktop={isDesktop} />
      <ProcessCard proc={globalMetrics.process} isDesktop={isDesktop} />
    </Flex>
  )
}

// ─── main ────────────────────────────────────────────────────────────────────

function GlobalItems() {
  const [globalAlertModalType, setGlobalAlertModalType] = useState('')
  const globalMetrics = useSelector((state) => state.globalClusters.globalMetrics)
  const globalAlerts = useSelector((state) => state.globalClusters.globalAlerts)
  const isDesktop = useSelector((state) => state.common.isDesktop)

  return (
    <Flex direction='column' gap='8px' className={styles.container}>
      <AccordionComponent
        heading='Server Metrics'
        body={<MetricsBody globalMetrics={globalMetrics} isDesktop={isDesktop} />}
      />
      <AccordionComponent
        heading='Global Alerts'
        body={<GlobalAlertsBody globalAlerts={globalAlerts} onOpenModal={setGlobalAlertModalType} />}
      />
      <AccordionComponent heading='Global Logs' body={<GlobalLogs />} />
      <AlertModal
        type={globalAlertModalType}
        isOpen={globalAlertModalType.length !== 0}
        closeModal={() => setGlobalAlertModalType('')}
        alerts={globalAlerts}
      />
    </Flex>
  )
}

export default GlobalItems
