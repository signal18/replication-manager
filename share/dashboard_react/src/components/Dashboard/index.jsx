import React, { useEffect } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import PageContainer from '../PageContainer'
import { getClusters } from '../../redux/clusterSlice'

function Dashboard(props) {
  console.log('inside dashboard')
  const dispatch = useDispatch()
  useEffect(() => {
    console.log('inside dashboard useeffect')
    dispatch(getClusters({}))
  }, [])

  return (
    <PageContainer>
      <div>Dashboard page</div>
    </PageContainer>
  )
}

export default Dashboard
