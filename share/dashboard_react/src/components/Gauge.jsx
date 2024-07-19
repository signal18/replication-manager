import { Box, Flex, Text } from '@chakra-ui/react'
import { useTheme } from '@emotion/react'
import React from 'react'
import GaugeComponent from 'react-gauge-component'

function Gauge({ value, text, width, containerSx }) {
  const styles = {
    container: {
      width: `${width}px`,
      position: 'relative',
      display: 'inline-block'
    },
    gauge: {
      width: '100%'
    },
    textOverlay: {
      position: 'absolute',
      bottom: '0%',
      left: '50%',
      transform: 'translate(-50%, -50%)',
      textAlign: 'center'
    },
    valueText: {
      display: 'block',
      fontSize: '15px'
    },
    labelText: {
      display: 'block',
      fontSize: '10px',
      fontWeight: 'bold'
    }
  }

  const formatValue = (value) => {
    if (typeof value === 'number' && !Number.isInteger(value)) {
      return value.toFixed(3)
    }
    return value
  }

  return (
    <Flex direction='column' justify='center'>
      <Box sx={styles.container}>
        <GaugeComponent
          style={styles.guage}
          value={value}
          labels={{
            valueLabel: {
              formatTextValue: () => '',
              style: styles.textValue,
              maxDecimalDigits: 3
            },
            tickLabels: { hideMinMax: true }
          }}
        />
        <Box sx={styles.textOverlay}>
          <Text sx={styles.valueText}>{formatValue(value)}</Text>
          <Text sx={styles.labelText}>{text}</Text>
        </Box>
      </Box>
      {/* <Text textAlign='center' fontWeight='500'>
        {text}
      </Text> */}
    </Flex>
  )
}

export default Gauge
