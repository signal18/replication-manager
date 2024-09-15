import React from 'react'
import { Bar, CartesianGrid, ComposedChart, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { useTheme } from '../../ThemeProvider'
import { Box } from '@chakra-ui/react'
import styles from './styles.module.scss'

function BarGraph({ data, className, graphName }) {
  const { theme } = useTheme()

  return (
    <Box className={`${styles.graphContainer} ${className}`}>
      <ResponsiveContainer width='100%' height='100%'>
        <ComposedChart
          layout='vertical'
          width={500}
          height={400}
          data={data}
          margin={{
            top: 20,
            right: 20,
            bottom: 20,
            left: 20
          }}>
          <CartesianGrid stroke={theme === 'light' ? '#e2e8f0' : '#2d3748'} />
          <XAxis type='number' label={{ value: graphName, position: 'insideTop', dy: 20, fontWeight: 'bold' }} />
          <YAxis dataKey='name' fontSize='1rem' width={80} type='category' scale='auto' />
          <Tooltip
            contentStyle={{ backgroundColor: theme === 'light' ? '#eff2fe' : '#131a34' }}
            itemStyle={{ color: theme === 'light' ? '#333333' : '#ffffff' }}
          />

          <Bar dataKey='value' label={''} barSize={20} fill={theme === 'light' ? '#3182ce' : '#2c5282'} />
        </ComposedChart>
      </ResponsiveContainer>
    </Box>
  )
}

export default BarGraph
