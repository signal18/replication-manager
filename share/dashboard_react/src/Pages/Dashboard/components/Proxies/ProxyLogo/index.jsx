import { Image, Tooltip } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'

function ProxyLogo({ proxyName }) {
  return (
    <Tooltip label={proxyName}>
      {proxyName === 'proxysql' ? (
        <Image className={styles.image} src='/images/proxysql.png' />
      ) : proxyName === 'haproxy' ? (
        <Image className={styles.image} src='/images/haproxy.png' />
      ) : (
        <Image className={styles.image} src='/images/genericproxy.svg' />
      )}
    </Tooltip>
  )
}

export default ProxyLogo
