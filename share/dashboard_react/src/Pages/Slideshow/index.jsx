import React, { useCallback, useEffect, useRef, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { Box, Flex, Progress, Text } from '@chakra-ui/react'
import {
  getBackupStats,
  getBackups,
  getClusterAlerts,
  getClusterApps,
  getClusterData,
  getClusterLogs,
  getClusterMaster,
  getClusterProxies,
  getClusterServers,
  getJobs,
  getResticCurrentTask,
  getResticSnapshot,
  setRefreshInterval
} from '../../redux/clusterSlice'
import { getClusters, getMonitoredData } from '../../redux/globalClustersSlice'
import ClusterDetail from '../Dashboard/components/ClusterDetail'
import HADetail from '../Dashboard/components/HADetail'
import DBServers from '../Dashboard/components/DBServers'
import Proxies from '../Dashboard/components/Proxies'
import Apps from '../Dashboard/components/Apps'
import { GeneralLogs, TaskLogs, SecurityLogs, WorkloadLogs } from '../Dashboard/components/Logs'
import Maintenance from '../Maintenance'
import PageContainer from '../PageContainer'
import AccordionComponent from '../../components/AccordionComponent'
import { AppSettings } from '../../AppSettings'

// Duration in milliseconds each slide is displayed
const SLIDE_DURATION_MS = 15000

// Sections shown per cluster in order
const SECTIONS = [
  { view: 'cluster-ha',         label: 'Cluster & HA' },
  { view: 'servers',            label: 'Servers' },
  { view: 'proxies',            label: 'Proxies' },
  { view: 'apps',               label: 'Application Servers' },
  { view: 'logs-cluster',       label: 'Cluster & Security Logs' },
  { view: 'logs-workload',      label: 'Workload Logs' },
  { view: 'maintenance-backup', label: 'Backup' },
  { view: 'maintenance-jobs',   label: 'Scheduler Jobs' },
  { view: 'logs-jobs',          label: 'Job Logs' },
]

const MAINTENANCE_VIEWS = new Set(['maintenance-backup', 'maintenance-jobs', 'logs-jobs'])

function Slideshow() {
  const dispatch = useDispatch()

  const clusters = useSelector((state) => state.globalClusters.clusters)
  const clusterData = useSelector((state) => state.cluster.clusterData)

  const [slides, setSlides] = useState([]) // [{clusterName, view, label}]
  const [slideIndex, setSlideIndex] = useState(0)
  const [progress, setProgress] = useState(0)
  const [user, setUser] = useState(null)

  // Refs for use inside intervals without stale closures
  const slidesRef = useRef([])
  const slideIndexRef = useRef(0)

  // ─── Load all data for a given slide ─────────────────────────────────────
  const loadSlideData = useCallback(
    (slide) => {
      if (!slide) return
      const { clusterName, view } = slide
      dispatch(getClusterData({ clusterName }))
      dispatch(getClusterServers({ clusterName }))
      dispatch(getClusterProxies({ clusterName }))
      dispatch(getClusterAlerts({ clusterName }))
      dispatch(getClusterMaster({ clusterName }))
      dispatch(getClusterLogs({ clusterName }))
      dispatch(getClusterApps({ clusterName }))
      if (MAINTENANCE_VIEWS.has(view)) {
        dispatch(getResticSnapshot({ clusterName, filter: 'latest-per-session' }))
        dispatch(getBackups({ clusterName }))
        dispatch(getBackupStats({ clusterName }))
        dispatch(getResticCurrentTask({ clusterName }))
        dispatch(getJobs({ clusterName }))
      }
    },
    [dispatch]
  )

  // ─── Build slides whenever cluster list changes ───────────────────────────
  useEffect(() => {
    if (!clusters || clusters.length === 0) return
    const built = []
    clusters.forEach((cl) => {
      const hasBackup =
        cl.config?.backupRestic ||
        (cl.config?.backupPhysicalType && cl.config?.backupPhysicalType !== 'none') ||
        (cl.config?.backupLogicalType && cl.config?.backupLogicalType !== 'none')
      const hasScheduler = cl.config?.schedulerEnabled
      const hasApps = cl.appServers?.length > 0

      SECTIONS.forEach(({ view, label }) => {
        if (MAINTENANCE_VIEWS.has(view) && !hasBackup && !hasScheduler) return
        if (view === 'apps' && !hasApps) return
        built.push({ clusterName: cl.name, view, label })
      })
    })
    slidesRef.current = built
    setSlides(built)
    setSlideIndex(0)
    slideIndexRef.current = 0
    loadSlideData(built[0])
  }, [clusters, loadSlideData])

  // ─── Initial cluster list fetch ───────────────────────────────────────────
  useEffect(() => {
    const interval = localStorage.getItem('refresh_interval')
      ? parseInt(localStorage.getItem('refresh_interval'))
      : AppSettings.DEFAULT_INTERVAL
    dispatch(setRefreshInterval({ interval }))
    dispatch(getClusters({}))
    dispatch(getMonitoredData({}))
  }, [])

  // ─── Slideshow timer ──────────────────────────────────────────────────────
  // Runs once on mount. Uses slidesRef so the interval never restarts when
  // the slides state updates, keeping the elapsed counter stable.
  useEffect(() => {
    let elapsed = 0
    const TICK_MS = 250

    const ticker = setInterval(() => {
      if (slidesRef.current.length === 0) return // clusters not loaded yet

      elapsed += TICK_MS
      setProgress(Math.min((elapsed / SLIDE_DURATION_MS) * 100, 100))

      if (elapsed >= SLIDE_DURATION_MS) {
        elapsed = 0
        setSlideIndex((prev) => {
          const next = (prev + 1) % slidesRef.current.length
          slideIndexRef.current = next
          loadSlideData(slidesRef.current[next])
          return next
        })
      }
    }, TICK_MS)

    return () => clearInterval(ticker)
  }, [loadSlideData])

  // ─── Background data refresh on a separate interval ───────────────────────
  useEffect(() => {
    const refreshMs =
      (localStorage.getItem('refresh_interval')
        ? parseInt(localStorage.getItem('refresh_interval'))
        : AppSettings.DEFAULT_INTERVAL) * 1000

    const poller = setInterval(() => {
      const current = slidesRef.current[slideIndexRef.current]
      if (current) loadSlideData(current)
    }, refreshMs)

    return () => clearInterval(poller)
  }, [loadSlideData])

  // ─── Resolve user from clusterData ────────────────────────────────────────
  useEffect(() => {
    if (clusterData?.apiUsers) {
      const username = localStorage.getItem('username')
      const apiUser = clusterData.apiUsers[username] || clusterData.apiUsers[username?.toLowerCase()]
      if (apiUser) setUser(apiUser)
    }
  }, [clusterData])

  const currentSlide = slides[slideIndex]
  const clusterCount = clusters?.length || 0
  // Derive per-cluster section position dynamically (clusters may have different section counts)
  const clusterNames = [...new Set(slides.map((s) => s.clusterName))]
  const clusterIndex = currentSlide ? clusterNames.indexOf(currentSlide.clusterName) + 1 : 0
  const slidesForCluster = slides.filter((s) => s.clusterName === currentSlide?.clusterName)
  const sectionIndex = currentSlide
    ? slidesForCluster.findIndex((s) => s.view === currentSlide.view) + 1
    : 0
  const totalSections = slidesForCluster.length

  return (
    <PageContainer>
      {/* ── Header bar ─────────────────────────────────────── */}
      <Flex
        px={6}
        py={3}
        align='center'
        justify='space-between'
        borderBottom='1px solid'
        borderColor='gray.200'>
        <Text fontWeight='bold' fontSize='3xl'>
          {currentSlide?.clusterName || '…'}
        </Text>
        <Text fontSize='2xl' fontWeight='semibold' color='gray.500'>
          {currentSlide?.label || '…'}
        </Text>
        <Text fontSize='sm' color='gray.400'>
          {clusterCount > 0
            ? `Cluster ${clusterIndex} of ${clusterCount} — section ${sectionIndex} of ${totalSections}`
            : 'Loading clusters…'}
        </Text>
      </Flex>

      {/* ── Progress bar ───────────────────────────────────── */}
      <Progress value={progress} size='xs' colorScheme='blue' borderRadius={0} />

      {/* ── Slide content ──────────────────────────────────── */}
      <Box px={4} py={4}>
        {slides.length === 0 && (
          <Text color='gray.400' textAlign='center' mt={8}>
            Loading clusters…
          </Text>
        )}

        {currentSlide?.view === 'cluster-ha' && clusterData && (
          <Flex gap='24px' direction={{ base: 'column', lg: 'row' }}>
            <ClusterDetail selectedCluster={clusterData} user={user} />
            <HADetail selectedCluster={clusterData} user={user} />
          </Flex>
        )}

        {currentSlide?.view === 'servers' && clusterData && (
          <DBServers selectedCluster={clusterData} user={user} />
        )}

        {currentSlide?.view === 'proxies' && clusterData && (
          <Proxies selectedCluster={clusterData} user={user} />
        )}

        {currentSlide?.view === 'apps' && clusterData && (
          <Apps selectedCluster={clusterData} user={user} />
        )}

        {currentSlide?.view === 'logs-cluster' && (
          <Flex direction='column' gap='8px'>
            <AccordionComponent heading='Cluster Logs' body={<GeneralLogs />} />
            <AccordionComponent heading='Security Logs' body={<SecurityLogs />} />
          </Flex>
        )}

        {currentSlide?.view === 'logs-workload' && (
          <AccordionComponent heading='Workload Logs' body={<WorkloadLogs />} />
        )}

        {currentSlide?.view === 'maintenance-backup' && (
          <Maintenance selectedCluster={clusterData} user={user} section='backup' />
        )}

        {currentSlide?.view === 'maintenance-jobs' && (
          <Maintenance selectedCluster={clusterData} user={user} section='jobs' />
        )}

        {currentSlide?.view === 'logs-jobs' && (
          <AccordionComponent heading='Job Logs' body={<TaskLogs />} />
        )}
      </Box>
    </PageContainer>
  )
}

export default Slideshow
