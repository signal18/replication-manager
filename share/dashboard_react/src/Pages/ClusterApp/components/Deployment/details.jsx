import { useDispatch } from 'react-redux'
import { Flex } from '@chakra-ui/react'
import { deploymentFieldChange, deploymentFieldIndexAdd, deploymentFieldIndexDrop } from '../../../../redux/clusterSlice'
import AccordionComponent from '../../../../components/AccordionComponent'
import Variables from './components/Variables'
import Paths from './components/Paths'
import GitClones from './components/GitClones'
import styles from './styles.module.scss'
import Routes from './components/Routes'
import { useCallback, useMemo } from 'react'

function DeploymentDetail({ clusterName, appId, row, dockerImage }) {
  const dispatch = useDispatch()

  const handleSaveArrayChange = useCallback((field, index, key, value) => {
    return dispatch(deploymentFieldChange({ clusterName, appId, field, index, key, value }))
  }, [clusterName, appId, dispatch]);

  const handleSaveAddItem = useCallback((field, value) => {
    return dispatch(deploymentFieldIndexAdd({ clusterName, appId, field, value }))
  }, [clusterName, appId, dispatch]);

  const handleDropIndex = useCallback((field, index) => {
    return dispatch(deploymentFieldIndexDrop({ clusterName, appId, field, index }))
  }, [clusterName, appId, dispatch]);

  const pathRows = useMemo(() => row?.paths || [], [row?.paths])
  const variableRows = useMemo(() => row?.variables || [], [row?.variables])
  const routeRows = useMemo(() => row?.routes || [], [row?.routes])
  const gitCloneRows = useMemo(() => row?.gitClones || [], [row?.gitClones])


  return (
    <Flex direction='column' gap='8px' w={'100%'} className={styles.contentContainer}>
      <AccordionComponent
        heading={'Routes'}
        body={<Routes rows={routeRows} fieldName={'routes'} onRowArrayChange={handleSaveArrayChange} onRowDropIndex={handleDropIndex} onSaveAdd={handleSaveAddItem} />}
      />
      <AccordionComponent
        heading={"Git Clones"}
        body={<GitClones rows={gitCloneRows} fieldName={'gitClones'} onRowArrayChange={handleSaveArrayChange} onRowDropIndex={handleDropIndex} onSaveAdd={handleSaveAddItem} />}
      />
      <AccordionComponent
        heading={'Paths'}
        body={<Paths clusterName={clusterName} appId={appId} dockerImage={dockerImage} rows={pathRows} gitCloneRows={gitCloneRows} fieldName={'path'} onRowArrayChange={handleSaveArrayChange} onRowDropIndex={handleDropIndex} onSaveAdd={handleSaveAddItem} />}
      />
      <AccordionComponent
        heading={'Variables'}
        body={<Variables rows={variableRows} fieldName={'variables'} onRowArrayChange={handleSaveArrayChange} onRowDropIndex={handleDropIndex} onSaveAdd={handleSaveAddItem} />}
      />
    </Flex>
  )
}

export default DeploymentDetail
