import { useDispatch } from 'react-redux'
import { Flex } from '@chakra-ui/react'
import { deploymentFieldChange, deploymentFieldIndexAdd, deploymentFieldIndexDrop, pauseAutoReload } from '../../../../redux/clusterSlice'
import AccordionComponent from '../../../../components/AccordionComponent'
import Variables from './components/Variables'
import Paths from './components/Paths'
import GitClones from './components/GitClones'
import styles from './styles.module.scss'
import Routes from './components/Routes'
import React, { useCallback, useMemo, useState } from 'react'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'

function useDeploymentActions(clusterName, appId, setDropIndex, setConfirmOpen) {
  const dispatch = useDispatch()

  const handleSaveArrayChange = useCallback(
    (field, index, key, value) => dispatch(deploymentFieldChange({ clusterName, appId, field, index, key, value })),
    [clusterName, appId, dispatch]
  )

  const handleSaveAddItem = useCallback(
    (field, value) => dispatch(deploymentFieldIndexAdd({ clusterName, appId, field, value })),
    [clusterName, appId, dispatch]
  )

  const handleDropIndex = useCallback(
    (field, index) => {
      setDropIndex({ field, index })
      setConfirmOpen(true)
    },
    [setDropIndex, setConfirmOpen]
  )

  const handlePauseAutoReload = useCallback(
    () => dispatch(pauseAutoReload({ isPaused: true })),
    [dispatch]
  )

  const handleResumeAutoReload = useCallback(
    () => dispatch(pauseAutoReload({ isPaused: false })),
    [dispatch]
  )

  return {
    handleSaveArrayChange,
    handleSaveAddItem,
    handleDropIndex,
    handlePauseAutoReload,
    handleResumeAutoReload
  }
}

const DeploymentDetail = ({ clusterName, appId, row, dockerImage, agentList }) => {
  const dispatch = useDispatch()
  const [isConfirmOpen, setConfirmOpen] = useState(false)
  const [dropIndex, setDropIndex] = useState(null)

  const { handleSaveArrayChange, handleSaveAddItem, handleDropIndex, handlePauseAutoReload, handleResumeAutoReload } = useDeploymentActions(clusterName, appId, setDropIndex, setConfirmOpen);

  const actionProps = useMemo(() => ({
    onRowArrayChange: handleSaveArrayChange,
    onSaveAdd: handleSaveAddItem,
    onRowDropIndex: handleDropIndex,
    onPauseAutoReload: handlePauseAutoReload,
    onResumeAutoReload: handleResumeAutoReload
  }), [handleSaveArrayChange, handleSaveAddItem, handleDropIndex, handlePauseAutoReload, handleResumeAutoReload]);

  const handleCloseConfirm = useCallback(() => {
    setConfirmOpen(false);
    setDropIndex(null);
  }, []);

  const handleConfirmDrop = useCallback(() => {
    if (dropIndex?.field != null && dropIndex?.index != null) {
      dispatch(deploymentFieldIndexDrop({ clusterName, appId, field: dropIndex.field, index: dropIndex.index }));
      handleCloseConfirm();
    }
  }, [clusterName, appId, dropIndex, dispatch]);

  const fieldRows = useMemo(() => ({
  routes: row?.routes || [],
  gitClones: row?.gitClones || [],
  paths: row?.paths || [],
  variables: row?.variables || [],
}), [row]);

  const dropConfirmText = useMemo(() => dropIndex?.field
    ? `Are you sure you want to remove this ${dropIndex.field} item? This action cannot be undone.`
    : "Are you sure you want to remove this item?", [dropIndex]);

  return (
    <Flex direction='column' gap='8px' w={'100%'} className={styles.contentContainer}>
      <AccordionComponent
        heading={'Routes'}
        body={<Routes rows={fieldRows.routes} fieldName={'routes'} {...actionProps} />}
      />
      <AccordionComponent
        heading={"Git Clones"}
        body={<GitClones rows={fieldRows.gitClones} fieldName={'gitClones'} {...actionProps} />}
      />
      <AccordionComponent
        heading={'Paths'}
        body={<Paths rows={fieldRows.paths} fieldName={'paths'} clusterName={clusterName} appId={appId} dockerImage={dockerImage} gitCloneRows={fieldRows.gitClones} {...actionProps} />}
      />
      <AccordionComponent
        heading={'Variables'}
        body={<Variables rows={fieldRows.variables} agentList={agentList} fieldName={'variables'} {...actionProps} />}
      />
      <ConfirmModal
        isOpen={isConfirmOpen}
        closeModal={handleCloseConfirm}
        onConfirmClick={handleConfirmDrop}
        title="Confirm Delete"
        body={dropConfirmText}
      />
    </Flex>
  )
}

export default React.memo(DeploymentDetail);