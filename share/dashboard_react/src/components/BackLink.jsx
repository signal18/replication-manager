import { Icon, Text, Link } from '@chakra-ui/react'
import React from 'react'
import { Link as ReactRouterLink } from 'react-router-dom'
import { HiArrowNarrowLeft } from 'react-icons/hi'
import { useSelector } from 'react-redux'

function BackLink({ path, ...rest }) {
  const {
    common: { theme }
  } = useSelector((state) => state)
  const styles = {
    backContainer: {
      display: 'flex',
      alignItems: 'center',
      p: '8px 16px',
      gap: '8px',
      fontSize: '16px',
      bg: theme === 'light' ? `blue.100` : `blue.800`,
      boxShadow: 'rgba(100, 100, 111, 0.2) 0px 7px 29px 0px;',
      width: 'fit-content',
      borderRadius: '12px',
      fontWeight: '500',
      _hover: {
        textDecoration: 'none'
      }
    }
  }
  return (
    <Link as={ReactRouterLink} sx={styles.backContainer} to={path} {...rest}>
      <Icon as={HiArrowNarrowLeft} fontSize={'1.5rem'} />
      <Text>Back</Text>
    </Link>
  )
}

export default BackLink
