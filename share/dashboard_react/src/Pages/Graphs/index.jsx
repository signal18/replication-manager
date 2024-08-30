import React, { useRef, useState, useEffect } from 'react'
import '../../styles/_graphite.scss'
import styles from './styles.module.scss'
import { Flex } from '@chakra-ui/react'
import Graphite from '../../components/Graphite'
import Dropdown from '../../components/Dropdown'

function Graphs() {
  const qpsRef = useRef()
  const coreRef = useRef()
  const netRef = useRef()
  const sbmRef = useRef()
  const [context, setContext] = useState(null)

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

  useEffect(() => {
    if (cubism) {
      setContext(cubism.context().serverDelay(5e3).clientDelay(5e3).step(selectedStep.value).size(selectedHour.value))
    }
    return () => {
      setContext(null)
    }
  }, [selectedHour, selectedStep])

  return (
    <Flex className={styles.graphContainer}>
      <Flex className={styles.filters}>
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
      </Flex>
    </Flex>
  )
}

export default Graphs
