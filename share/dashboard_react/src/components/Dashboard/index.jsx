import React, { useEffect } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import PageContainer from '../PageContainer'
import { getClusters } from '../../redux/clusterSlice'
import Tabs from '../Tabs'

function Dashboard(props) {
  const dispatch = useDispatch()
  useEffect(() => {
    dispatch(getClusters({}))
  }, [])

  return (
    <PageContainer>
      <div>
        <Tabs />
      </div>
    </PageContainer>
  )
}

export default Dashboard
