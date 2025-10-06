import { Box, Flex, Text } from '@chakra-ui/react'
import React from 'react'
import Gauge from '../../../../components/Gauge';
import Card from '../../../../components/Card';
import { useSelector } from 'react-redux';
import TagPill from '../../../../components/TagPill';
import styles from './styles.module.scss';
import { convertSize } from '../../../../utility/common';

const getScoreColor = (score) => {
  if (score >= 80) return 'green';
  if (score >= 60) return 'orange';
  return 'red';
};

const getLoadColor = (load, cores) => {
  const percent = (load / cores) * 100;
  if (percent < 70) return 'green';
  if (percent < 90) return 'orange';
  return 'red';
};

const OpenSVCNodeCard = ({ nodeData }) => {
  const isDesktop = useSelector((state) => state.common.isDesktop)

  const { node, stats, cores } = nodeData;
  const loadPercent = ((stats.load_15m / cores) * 100).toFixed(1);
  const cpuLoadColor = getLoadColor(stats.cpuLoad, node.cpuCores);
  const memUsed = stats.mem_total - stats.mem_avail;
  const swapUsed = stats.swap_total - stats.swap_avail;

  return (
    <Card
      width={isDesktop ? '30%' : '100%'}
      header={
        <>
          <Text>{node}</Text>
          <Box ml='auto'>
            <TagPill colorScheme={getScoreColor(stats.score)} text={`${stats.score}`} />
          </Box>
        </>
      }
      body={
        <Flex direction='column' gap='16px'>
          <Flex direction='column' gap='16px'>
            <Flex justify='space-between' align='center'>
              <Text>Load Average (15m)</Text>
              <Text>{stats.load_15m}/{cores} ({loadPercent}%)</Text>
            </Flex>
            <Box width='100%' height='10px' bg='gray.200' borderRadius='5px' overflow='hidden'>
              <Box
                height='100%'
                bg={cpuLoadColor}
                width={`${Math.min(loadPercent, 100)}%`}
                transition='width 0.3s ease-in-out'
              />
            </Box>
          </Flex>
          <Flex justify='space-around'>
            <Gauge
              minValue={0}
              maxValue={stats.mem_total}
              value={memUsed}
              text={'Memory'}
              width={180}
              height={135}
              isDisabled={true}
              isGaugeSizeCustomized={false}
              hideMinMax={false}
              textOverlayClassName={styles.textOverlay}
            />
            <Gauge
              minValue={0}
              maxValue={stats.swap_total}
              value={swapUsed}
              text={'Swap'}
              width={180}
              height={135}
              isDisabled={true}
              isGaugeSizeCustomized={false}
              hideMinMax={false}
              textOverlayClassName={styles.textOverlay}
            />
          </Flex>
        </Flex>
      }
    />
  )
}

const OpenSVCWorkload = function ({ workload }) {
  return (
    <Flex direction={'column'} gap='16px'>
      <Flex wrap='wrap' gap='16px' align='center' justify='space-evenly'>
        {workload.map((nodeData) => (
          <OpenSVCNodeCard key={nodeData.node} nodeData={nodeData} />
        ))}
      </Flex>
    </Flex>
  )
}

export default OpenSVCWorkload
