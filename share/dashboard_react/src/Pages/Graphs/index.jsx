import React, { useRef, useState, useEffect } from 'react'
import '../../styles/_graphite.scss'
import styles from '../../styles/Graphs.module.scss';
import { Flex } from '@chakra-ui/react'
import Graphite from '../../components/Graphite'
import Dropdown from '../../components/Dropdown'
import ChartLatchTracing from '../../components/ChartLatchTracing';
import ChartMultiMetric from '../../components/ChartMultiMetric';
import ChartBarStack from '../../components/ChartBarStack';
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
        <Graphite
          chartRef={qpsRef}
          size={selectedHour.value}
          step={selectedStep.value}
          context={context}
          title={'Qps'}
          target={'perSecond(mysql.*.mysql_global_status_queries)'}
          className={`${styles.graph} ${styles.qpsGraph} ${styles[`width${selectedHour.value}`]}`}
        />
        <Graphite
          chartRef={coreRef}
          size={selectedHour.value}
          step={selectedStep.value}
          context={context}
          title={'Threads'}
          target={'sumSeries(mysql.*.mysql_global_status_threads_running)'}
          maxExtent={1024}
          className={`${styles.graph}  ${styles[`width${selectedHour.value}`]}`}
        />
        <Graphite
          chartRef={netRef}
          size={selectedHour.value}
          step={selectedStep.value}
          context={context}
          title={'BytesIn'}
          target={'perSecond(mysql.*.mysql_global_status_bytes_received)'}
          title2={'BytesOut'}
          target2={'perSecond(mysql.*.mysql_global_status_bytes_sent)'}
          maxExtent={100000}
          className={`${styles.graph}  ${styles[`width${selectedHour.value}`]}`}
        />
        <Graphite
          chartRef={sbmRef}
          size={selectedHour.value}
          step={selectedStep.value}
          context={context}
          title={'ReplDelay'}
          target={'sumSeries(mysql.*.mysql_slave_status_seconds_behind_master)'}
          maxExtent={8000}
          className={`${styles.graph}  ${styles[`width${selectedHour.value}`]}`}
        />
        <ChartLatchTracing
          context={context}
          title={'Mutex'}
          metricPaths={[
            'maxSeries(mysql.*.mysql_global_status_wait_synch_mutex_innodb_buf_pool_mutex)',
            'maxSeries(mysql.*.mysql_global_status_wait_synch_mutex_innodb_buf_dblwr_mutex)',
            'maxSeries(mysql.*.mysql_global_status_wait_synch_mutex_innodb_fil_system_mutex)',
            'maxSeries(mysql.*.mysql_global_status_wait_synch_mutex_innodb_flush_list_mutex)',
            'maxSeries(mysql.*.mysql_global_status_wait_synch_mutex_innodb_lock_wait_mutex)',
            'maxSeries(mysql.*.mysql_global_status_wait_synch_mutex_innodb_trx_sys_mutex)'
          ]}
          className={`${styles.graph} ${styles[`width${selectedHour.value}`]}`}
          isVisible={selectedCluster.config.monitoringPerformanceSchemaMutex}
        />
        <ChartLatchTracing
          context={context}
          title={'latch'}
         metricPaths={[
         'sumSeries(mysql.*.mysql_global_status_wait_synch_rwlock_innodb_btr_search_latch)',
         'sumSeries(mysql.*.mysql_global_status_wait_synch_rwlock_innodb_fil_space_latch)',
         'sumSeries(mysql.*.mysql_global_status_wait_synch_rwlock_innodb_trx_purge_latch)',
         'sumSeries(mysql.*.mysql_global_status_wait_synch_rwlock_innodb_trx_rseg_latch)',
         'sumSeries(mysql.*.mysql_global_status_wait_synch_rwlock_innodb_lock_latch)',
         'sumSeries(mysql.*.mysql_global_status_wait_synch_rwlock_innodb_log_latch)'
         ]}
          className={`${styles.graph} ${styles[`width${selectedHour.value}`]}`}
          isVisible={selectedCluster.config.monitoringPerformanceSchemaLatch}
        />
        <ChartBarStack
          context={context}
          title={'Memory'}
          metricPaths={[
            'maxSeries(mysql.*.mysql_global_status_performance_schema_memory)',
            'maxSeries(mysql.*.mysql_global_status_memory_used)',
            'maxSeries(mysql.*.mysql_global_status_innodb_buffer_pool_bytes_data)',
            'maxSeries(mysql.*.mysql_global_status_aria_pagecache_bytes_data)'
          ]}
          className={`${styles.graph} ${styles.qpsGraph} ${styles[`width${selectedHour.value}`]}`}
        />
        <ChartMultiMetric
         context={context}
         metricPaths={[
           'maxSeries(mysql.*.mysql_global_status_innodb_checkpoint_age)',
           'averageSeries(mysql.*.mysql_global_variables_innodb_log_file_size)'
         ]}
         height={300}
         className={`${styles.graph} ${styles.multiMetricGraph}`}
         title="InnoDB Redo Log Status"
       />
       <Graphite
         chartRef={ihlRef}
         size={selectedHour.value}
         step={selectedStep.value}
         context={context}
        maxExtent={100000}
         title={'InnodbHistoryListLenght'}
         target={'maxSeries(mysql.*.engine_innodb_history_list_lenght_inside_innodb)'}
         className={`${styles.graph}  ${styles[`width${selectedHour.value}`]}`}
       />
       <Graphite
         chartRef={irvef}
         size={selectedHour.value}
         step={selectedStep.value}
         context={context}
         maxExtent={100000}
         title={'InnodbReadViews'}
         target={'maxSeries(mysql.*.engine_innodb_read_views_open_inside_innodb)'}
         className={`${styles.graph}  ${styles[`width${selectedHour.value}`]}`}
       />

      </Flex>
      )}
    </Flex>
  )
}

export default Graphs
