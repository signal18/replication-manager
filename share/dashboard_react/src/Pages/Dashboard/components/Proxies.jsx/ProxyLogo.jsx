import { Image, Tooltip } from '@chakra-ui/react'
import React from 'react'

function ProxyLogo({ proxyName }) {
  const styles = {
    image: {
      width: '32px',
      height: '32px',
      margin: 'auto',
      objectFit: 'cover'
    }
  }
  return (
    <Tooltip label={proxyName}>
      {proxyName === 'proxysql' ? (
        <Image sx={styles.image} src='/images/proxysql.png' />
      ) : proxyName === 'haproxy' ? (
        <Image sx={styles.image} src='/images/haproxy.png' />
      ) : (
        <Image sx={styles.image} src='/images/genericproxy.svg' />
      )}
    </Tooltip>
  )
}

export default ProxyLogo
