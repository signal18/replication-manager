import { Box, Text } from '@chakra-ui/react'
import { useTheme } from '@emotion/react'
import React from 'react'
import GaugeComponent from 'react-gauge-component'

function Gauge({ value, text }) {
  const styles = {
    container: {
      // width: '200px'
    },
    textValue: {
      textShadow: 'none'
    }
  }

  return (
    <Box>
      <GaugeComponent
        style={styles.container}
        pointer={{ type: 'needle', color: 'red', length: 0.7 }}
        value={value}
        labels={{
          valueLabel: { formatTextValue: (value) => value, style: styles.textValue, maxDecimalDigits: 3 }
        }}
      />
      <Text textAlign='center' fontWeight='500'>
        {text}
      </Text>
    </Box>
  )
}

export default Gauge
