import { Box, Flex, Text } from '@chakra-ui/react'
import React, { useEffect, useRef } from 'react'
import GaugeComponent from 'react-gauge-component'
import styles from './styles.module.scss'

function Gauge({ value, text, width, height, className }) {
  const svgRef = useRef(null)

  const updateGaugePosition = () => {
    const svgElement = svgRef.current.querySelector('svg')
    if (svgElement) {
      const gElements = svgElement.querySelectorAll('g')
      gElements.forEach((g) => {
        const transform = g.getAttribute('transform')
        const translateMatch = /translate\(([^,]+),\s*([^)]+)\)/.exec(transform)
        if (translateMatch) {
          const [_, x, y] = translateMatch
          g.setAttribute('transform', `translate(${x}, 30)`)
        }
      })
    }
  }

  useEffect(() => {
    updateGaugePosition()
  }, [value, updateGaugePosition])

  useEffect(() => {
    window.addEventListener('resize', updateGaugePosition)
    return () => {
      window.removeEventListener('resize', updateGaugePosition)
    }
  }, [])

  const formatValue = (value) => {
    if (typeof value === 'number' && !Number.isInteger(value)) {
      return value.toFixed(3)
    }
    return value
  }

  return (
    <Flex direction='column' justify='center'>
      <Box width={width} height={height} className={`${styles.container} ${className}`} ref={svgRef}>
        <GaugeComponent
          className={styles.guage}
          value={value}
          labels={{
            valueLabel: {
              formatTextValue: () => '',
              maxDecimalDigits: 3
            },
            tickLabels: { hideMinMax: true }
          }}
        />
        <Box className={styles.textOverlay}>
          <Text className={styles.valueText}>{formatValue(value)}</Text>
          <Text className={styles.labelText}>{text}</Text>
        </Box>
      </Box>
    </Flex>
  )
}

export default Gauge
