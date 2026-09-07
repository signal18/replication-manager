import React, { useRef, useState, useEffect } from 'react'
import '../../styles/_graphite.scss'
import styles from '../../styles/Graphs.module.scss';
import { Flex } from '@chakra-ui/react'
import Graphite from '../../components/Graphite'
import Dropdown from '../../components/Dropdown'
import ChartLatchTracing from '../../components/ChartLatchTracing';
import ChartMultiMetric from '../../components/ChartMultiMetric';
import ChartGroupedDBU from '../../components/ChartGroupedDBU';
import ChartBarStack from '../../components/ChartBarStack'
import ChartTimeSeriesLine from '../../components/ChartTimeSeriesLine';
import RMIconButton from '../../components/RMIconButton'
import { HiCog } from 'react-icons/hi'

function Graphs({ selectedCluster, onOpenSettings }) {
  const qpsRef = useRef()
  const coreRef = useRef()
  const netRef = useRef()
  const sbmRef = useRef()
  const ihlRef = useRef()
  const irvef = useRef()



  const [hourOptions, setHourOptions] = useState([
    { name: '1 hour', value: 360 },
    { name: '2 hours', value: 720 },
    { name: '3 hours', value: 1080 },
    { name: '4 hours', value: 1440 },
    { name: '6 hours', value: 2160 },
    { name: '8 hours', value: 2880 },
    { name: '12 hours', value: 4320 }
  ])

  const [stepOptions, setStepOptions] = useState([
    { name: '5 seconds', value: 5e3 },
    { name: '10 seconds', value: 1e4 },
    { name: '15 seconds', value: 1.5e4 },
    { name: '30 seconds', value: 3e4 },
    { name: '1 minute', value: 6e4 },
    { name: '2 minutes', value: 1.2e5 }
  ])
  const [selectedHour, setSelectedHour] = useState({ name: '2 hours', value: 720 })
  const [selectedStep, setSelectedStep] = useState({ name: '10 seconds', value: 1e4 })
  //console.log("selectedCluster:", selectedCluster);
  const [context, setContext] = useState(null)

  // Scope every graphite target to the servers of the selected cluster.
  // The carbon metric host is the DB HOSTNAME uppercased with '.' -> '-'
  // (see cluster/srv_snd.go graphiteHostname). Without this the page used the
  // bare 'mysql.*' wildcard, so maxSeries/sumSeries aggregated across the WHOLE
  // fleet and a cluster's graph showed another cluster's numbers (#1756).
  const carbonHost = (h) =>
    (h || '').toUpperCase().replace(/[`?()'"<]/g, '-').replace(/\./g, '-').replace(/[ /]/g, '_')
  // Scope a whole-fleet 'mysql.*' to THIS cluster by its NAME, which is embedded in
  // every carbon host id ('DB<n>-<CLUSTER>-SVC-CLOUD18'): match 'mysql.*-<CLUSTER>-*'.
  // ONE wildcard pattern -- NOT a '{a,b,c}' brace, which go-graphite expands into several
  // SEPARATE series that ChartMultiMetric (one series per target) cannot parse -> blank
  // graph (#1756 regression). So maxSeries/sumSeries aggregate to ONE series. It also
  // filters by cluster ALWAYS, with NO dependency on the (possibly not-yet-loaded) server
  // list, so it never falls back to the unscoped whole-fleet '*' that mixes clusters.
  const clusterToken = carbonHost(selectedCluster?.name || '')
  const scope = (s) =>
    typeof s === 'string' && clusterToken ? s.replaceAll('mysql.*', `mysql.*-${clusterToken}-*`) : s
  const scopeAll = (a) => (Array.isArray(a) ? a.map(scope) : a)

  const cfg = selectedCluster?.config || {}
  // The plan line = prov-service-plan-dbu, a REAL config field repman materializes (sum of
  // per-server DBU when the admin hasn't set it / on-premise). The GUI just READS it.
  const planDbu = parseInt(cfg.provServicePlanDbu) || 1

  // Window (seconds) and refresh cadence for the d3 line charts, from the same
  // hour/step selectors that drive the cubism graphs.
  const windowSec = Math.max(60, Math.round((selectedHour.value * selectedStep.value) / 1000))
  const refreshMs = selectedStep.value

  // Seconds -> human duration, for the replication-delay Y axis (values are seconds,
  // labelled 1s/1m/1h/1d up to the 6-day cap).
  const fmtDur = (s) => {
    if (s < 60) return `${Math.round(s)}s`
    if (s < 3600) return `${Math.round(s / 60)}m`
    if (s < 86400) return `${Math.round(s / 3600)}h`
    return `${Math.round(s / 86400)}d`
  }

  useEffect(() => {
  if (typeof window === 'undefined' || !window.cubism) return;

  const newContext = window.cubism.context()
    .serverDelay(0)  // Match your working components
    .clientDelay(0)
    .step(selectedStep.value)
    .size(selectedHour.value);

  // Critical change: Start the context immediately
  newContext.start();


  setContext(newContext);

  return () => {
    newContext.stop();
    setContext(null);
  };
}, [selectedHour.value, selectedStep.value]);

    return (
    <Flex className={styles.graphContainer}>
      <Flex className={styles.filters}>
        {onOpenSettings && (
          <RMIconButton icon={HiCog} tooltip='Graph Settings' onClick={onOpenSettings} size='sm' variant='ghost' />
        )}
        <Dropdown
          label={'Last N hours'}
          options={hourOptions}
          selectedValue={selectedHour.value}
          onChange={(value) => {
            setSelectedHour(value)
          }}
        />
        <Dropdown
          label={'Steps'}
          options={stepOptions}
          selectedValue={selectedStep.value}
          onChange={(value) => {
            setSelectedStep(value)
          }}
        />
      </Flex>
      { context && (
      <Flex className={styles.graphs}>
        <ChartTimeSeriesLine
          title='Qps'
          yLabel='queries/s'
          logScale
          cap={1e6}
          windowSec={windowSec}
          refreshMs={refreshMs}
          targets={[{ target: scope('sumSeries(perSecond(mysql.*.mysql_global_status_queries))'), label: 'Qps' }]}
          className={`${styles.graph} ${styles.qpsGraph} ${styles[`width${selectedHour.value}`]}`}
        />
        <ChartTimeSeriesLine
          title='Threads running'
          yLabel='threads'
          logScale
          logBase={2}
          cap={1024}
          windowSec={windowSec}
          refreshMs={refreshMs}
          targets={[{ target: scope('sumSeries(mysql.*.mysql_global_status_threads_running)'), label: 'Threads' }]}
          className={`${styles.graph}  ${styles[`width${selectedHour.value}`]}`}
        />
        <ChartTimeSeriesLine
          title='Network in / out'
          yLabel='bytes/s'
          logScale
          cap={100e9}
          windowSec={windowSec}
          refreshMs={refreshMs}
          targets={[
            { target: scope('sumSeries(perSecond(mysql.*.mysql_global_status_bytes_received))'), label: 'In' },
            { target: scope('sumSeries(perSecond(mysql.*.mysql_global_status_bytes_sent))'), label: 'Out' }
          ]}
          className={`${styles.graph}  ${styles[`width${selectedHour.value}`]}`}
        />
        <ChartTimeSeriesLine
          title='Replication delay'
          yLabel='behind master'
          logScale
          cap={518400}
          yTickValues={[1, 10, 60, 600, 3600, 21600, 86400, 259200, 518400]}
          yTickFormat={fmtDur}
          windowSec={windowSec}
          refreshMs={refreshMs}
          targets={[{ target: scope('sumSeries(mysql.*.mysql_slave_status_seconds_behind_master)'), label: 'Delay' }]}
          className={`${styles.graph}  ${styles[`width${selectedHour.value}`]}`}
        />
        <ChartGroupedDBU
         context={context}
         dbuPaths={{
           cpu: scope('sumSeries(mysql.*.dbu_cpu)'),
           mem: scope('sumSeries(mysql.*.dbu_mem)'),
           io: scope('sumSeries(mysql.*.dbu_io)'),
           disk: scope('sumSeries(mysql.*.dbu_disk)')
         }}
         servicePaths={{
           cpu: scope('sumSeries(mysql.*.service_cpu)'),
           mem: scope('sumSeries(mysql.*.service_mem)'),
           io: scope('sumSeries(mysql.*.service_io)'),
           disk: scope('sumSeries(mysql.*.service_disk)')
         }}
         pivotPath={scope('sumSeries(mysql.*.dbu)')}
         planDbu={planDbu}
         height={300}
         className={`${styles.graph} ${styles.multiMetricGraph}`}
         title="Consumed DBU — real → DBU per axis (plan = configurator)"
       />
        <ChartMultiMetric
         context={context}
         metricPaths={scopeAll([
           'maxSeries(mysql.*.mysql_global_status_innodb_checkpoint_age)',
           'averageSeries(mysql.*.mysql_global_variables_innodb_log_file_size)'
         ])}
         height={300}
         className={`${styles.graph} ${styles.multiMetricGraph}`}
         title="InnoDB Redo Log Status"
       />
        <ChartLatchTracing
          context={context}
          title={'Mutex'}
          metricPaths={scopeAll([
            'maxSeries(mysql.*.mysql_global_status_wait_synch_mutex_innodb_buf_pool_mutex)',
            'maxSeries(mysql.*.mysql_global_status_wait_synch_mutex_innodb_buf_dblwr_mutex)',
            'maxSeries(mysql.*.mysql_global_status_wait_synch_mutex_innodb_fil_system_mutex)',
            'maxSeries(mysql.*.mysql_global_status_wait_synch_mutex_innodb_flush_list_mutex)',
            'maxSeries(mysql.*.mysql_global_status_wait_synch_mutex_innodb_lock_wait_mutex)',
            'maxSeries(mysql.*.mysql_global_status_wait_synch_mutex_innodb_trx_sys_mutex)'
          ])}
          className={`${styles.graph} ${styles[`width${selectedHour.value}`]}`}
          isVisible={selectedCluster.config.monitoringPerformanceSchemaMutex}
        />
        <ChartLatchTracing
          context={context}
          title={'latch'}
         metricPaths={scopeAll([
         'sumSeries(mysql.*.mysql_global_status_wait_synch_rwlock_innodb_btr_search_latch)',
         'sumSeries(mysql.*.mysql_global_status_wait_synch_rwlock_innodb_fil_space_latch)',
         'sumSeries(mysql.*.mysql_global_status_wait_synch_rwlock_innodb_trx_purge_latch)',
         'sumSeries(mysql.*.mysql_global_status_wait_synch_rwlock_innodb_trx_rseg_latch)',
         'sumSeries(mysql.*.mysql_global_status_wait_synch_rwlock_innodb_lock_latch)',
         'sumSeries(mysql.*.mysql_global_status_wait_synch_rwlock_innodb_log_latch)'
         ])}
          className={`${styles.graph} ${styles[`width${selectedHour.value}`]}`}
          isVisible={selectedCluster.config.monitoringPerformanceSchemaLatch}
        />
        <ChartBarStack
          context={context}
          title={'Memory'}
          metricPaths={scopeAll([
            'maxSeries(mysql.*.mysql_global_status_performance_schema_memory)',
            'maxSeries(mysql.*.mysql_global_status_memory_used)',
            'maxSeries(mysql.*.mysql_global_status_innodb_buffer_pool_bytes_data)',
            'maxSeries(mysql.*.mysql_global_status_aria_pagecache_bytes_data)'
          ])}
          className={`${styles.graph} ${styles.qpsGraph} ${styles[`width${selectedHour.value}`]}`}
        />
       <Graphite
         chartRef={ihlRef}
         size={selectedHour.value}
         step={selectedStep.value}
         context={context}
        maxExtent={100000}
         title={'InnodbHistoryListLenght'}
         target={scope('maxSeries(mysql.*.engine_innodb_history_list_lenght_inside_innodb)')}
         className={`${styles.graph}  ${styles[`width${selectedHour.value}`]}`}
       />
       <Graphite
         chartRef={irvef}
         size={selectedHour.value}
         step={selectedStep.value}
         context={context}
         maxExtent={100000}
         title={'InnodbReadViews'}
         target={scope('maxSeries(mysql.*.engine_innodb_read_views_open_inside_innodb)')}
         className={`${styles.graph}  ${styles[`width${selectedHour.value}`]}`}
       />

      </Flex>
      )}
    </Flex>
  )
}

export default Graphs
