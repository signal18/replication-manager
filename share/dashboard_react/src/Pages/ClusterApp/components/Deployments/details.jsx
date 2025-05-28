import { useDispatch } from 'react-redux'
import { Flex } from '@chakra-ui/react'
import { deploymentFieldChange, deploymentFieldIndexAdd, deploymentFieldIndexDrop } from '../../../../redux/clusterSlice'
import AccordionComponent from '../../../../components/AccordionComponent'
import GeneralSection from './components/GeneralSection'
import Variables from './components/Variables'
import Paths from './components/Paths'
import Ports from './components/Ports'
import GitClones from './components/GitClones'

function DeploymentDetail({ clusterName, appId, row, deployId }) {
  const dispatch = useDispatch()

  const handleInputChange = (field, value) => {
    dispatch(deploymentFieldChange({ clusterName, appId, deployId, field, value }))
  };

  const handleSaveArrayChange = (field, index, key, value) => {
    dispatch(deploymentFieldChange({ clusterName, appId, deployId, field, index, key, value }))
  };

  const handleSaveAddItem = (field, value) => dispatch(deploymentFieldIndexAdd({ clusterName, appId, deployId, field, value }))

  const handleDropIndex = (field, index) => {
    dispatch(deploymentFieldIndexDrop({ clusterName, appId, deployId, field, index }))
  };

  return (
    <Flex direction='column' gap='8px'>
      <AccordionComponent
        heading={'General Section'}
        body={<GeneralSection row={row} onChange={handleInputChange} />}
      />
      <AccordionComponent
        heading={'Variables'}
        body={<Variables rows={row?.variables || []} fieldName={'variables'} onRowArrayChange={handleSaveArrayChange} onRowDropIndex={handleDropIndex} onSaveAdd={handleSaveAddItem} />}
      />
      <AccordionComponent
        heading={'Paths'}
        body={<Paths rows={row?.path || []} fieldName={'path'} onRowArrayChange={handleSaveArrayChange} onRowDropIndex={handleDropIndex} onSaveAdd={handleSaveAddItem} />}
        />
      <AccordionComponent
        heading={'Ports'}
        body={<Ports rows={row?.ports || []} fieldName={'ports'} onRowArrayChange={handleSaveArrayChange} onRowDropIndex={handleDropIndex} onSaveAdd={handleSaveAddItem} />}
      />
      <AccordionComponent
        heading={"Git Clones"}
        body={<GitClones rows={row?.gitClones || []} fieldName={'gitClones'} onRowArrayChange={handleSaveArrayChange} onRowDropIndex={handleDropIndex} onSaveAdd={handleSaveAddItem} />}
      />
    </Flex>
  )
}

export default DeploymentDetail
