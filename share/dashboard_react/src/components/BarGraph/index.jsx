import React from 'react'
import {
  Area,
  Bar,
  BarChart,
  CartesianGrid,
  ComposedChart,
  Legend,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis
} from 'recharts'
import { useTheme } from '../../ThemeProvider'
import { Box } from '@chakra-ui/react'
import styles from './styles.module.scss'

function BarGraph({ data, className }) {
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
          <XAxis type='number' label={null} />
          <YAxis dataKey='name' type='category' scale='auto' />
          <Tooltip
            contentStyle={{ backgroundColor: theme === 'light' ? '#eff2fe' : '#131a34' }}
            itemStyle={{ color: theme === 'light' ? '#333333' : '#ffffff' }}
          />
          <Legend />
          {/* <Area type='monotone' dataKey='value' fill='#8884d8' stroke='#8884d8' /> */}
          <Bar dataKey='value' name='' barSize={20} fill={theme === 'light' ? '#3182ce' : '#2c5282'} />
          {/* <Line type='monotone' dataKey='value' stroke='#ff7300' /> */}
        </ComposedChart>
      </ResponsiveContainer>
    </Box>
  )
}

export default BarGraph
