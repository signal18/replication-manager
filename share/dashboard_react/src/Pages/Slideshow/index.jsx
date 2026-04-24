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
import Dashboard from '../Dashboard'
import Maintenance from '../Maintenance'
import PageContainer from '../PageContainer'
import { AppSettings } from '../../AppSettings'

// Duration in milliseconds each slide is displayed
const SLIDE_DURATION_MS = 30000

function Slideshow() {
  const dispatch = useDispatch()

  const clusters = useSelector((state) => state.globalClusters.clusters)
  const clusterData = useSelector((state) => state.cluster.clusterData)

  const [slides, setSlides] = useState([]) // [{clusterName, view}]
  const [slideIndex, setSlideIndex] = useState(0)
  const [progress, setProgress] = useState(0)
  const [user, setUser] = useState(null)

  // Refs for use inside intervals without stale closures
  const slidesRef = useRef([])
  const slideIndexRef = useRef(0)

  // ─── Build slides whenever cluster list changes ───────────────────────────
  useEffect(() => {
    if (!clusters || clusters.length === 0) return
    const built = []
    clusters.forEach((cl) => {
      built.push({ clusterName: cl.name, view: 'dashboard' })
      built.push({ clusterName: cl.name, view: 'maintenance' })
    })
    slidesRef.current = built
    setSlides(built)
    // Reset to first slide when cluster list changes
    setSlideIndex(0)
    slideIndexRef.current = 0
  }, [clusters])

  // ─── Initial cluster list fetch ───────────────────────────────────────────
  useEffect(() => {
    const interval = localStorage.getItem('refresh_interval')
      ? parseInt(localStorage.getItem('refresh_interval'))
      : AppSettings.DEFAULT_INTERVAL
    dispatch(setRefreshInterval({ interval }))
    dispatch(getClusters({}))
    dispatch(getMonitoredData({}))
  }, [])

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
      if (view === 'maintenance') {
        dispatch(getResticSnapshot({ clusterName, filter: 'latest-per-session' }))
        dispatch(getBackups({ clusterName }))
        dispatch(getBackupStats({ clusterName }))
        dispatch(getResticCurrentTask({ clusterName }))
        dispatch(getJobs({ clusterName }))
      }
    },
    [dispatch]
  )

  // ─── Slideshow timer ──────────────────────────────────────────────────────
  useEffect(() => {
    if (slides.length === 0) return

    // Load data for the first slide immediately
    loadSlideData(slides[0])

    let elapsed = 0
    const TICK_MS = 250

    const ticker = setInterval(() => {
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
  }, [slides, loadSlideData])

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
  const totalSlides = slides.length
  const clusterCount = clusters?.length || 0

  return (
    <PageContainer>
      {/* ── Header bar ─────────────────────────────────────── */}
      <Flex
        px={4}
        py={2}
        align='center'
        justify='space-between'
        borderBottom='1px solid'
        borderColor='gray.200'>
        <Text fontWeight='bold' fontSize='lg'>
          {currentSlide?.clusterName || '…'}
        </Text>
        <Text fontSize='sm' textTransform='capitalize' color='gray.500'>
          {currentSlide?.view === 'dashboard' ? 'Dashboard' : 'Maintenance'}
        </Text>
        <Text fontSize='xs' color='gray.400'>
          {clusterCount > 0
            ? `Cluster ${Math.floor(slideIndex / 2) + 1} of ${clusterCount}`
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
        {currentSlide?.view === 'dashboard' && (
          <Dashboard selectedCluster={clusterData} user={user} />
        )}
        {currentSlide?.view === 'maintenance' && (
          <Maintenance selectedCluster={clusterData} user={user} />
        )}
      </Box>
    </PageContainer>
  )
}

export default Slideshow
