import { Box, VStack, Heading, Flex } from "@chakra-ui/react";
import GitCloneSection from "./GitClone";
import VolumeSection from "./Volume";
import S3DirectorySection from "./S3Directory";
import styles from "./styles.module.scss";
import { useCallback, useMemo, useState } from "react";
import { useDispatch, useSelector } from "react-redux";
import AccordionComponent from "../../../../components/AccordionComponent";
import ConfirmModal from "../../../../components/Modals/ConfirmModal";
import { pauseAutoReload, storageFieldChange, storageFieldIndexAdd, storageFieldIndexDrop } from "../../../../redux/clusterSlice";

export default function StoragePage({ clusterName, appId, user }) {
  const dispatch = useDispatch();
  const storages = useSelector((state) => state.cluster?.app?.deployment?.storages);
  const volumePools = useSelector((state) => state.cluster?.clusterData?.config?.provAppVolumePools);

  const [modalState, setModalState] = useState({
    isOpen: false,
    field: null,
    index: null,
  })

  useMemo(() => {
    if (!storages) {
      return;
    }
  }, [storages]);

  const { isOpen: isConfirmOpen, field, index } = modalState;

  const dropConfirmText = useMemo(() => field
    ? `Are you sure you want to remove this ${field} item? This action cannot be undone.`
    : "Are you sure you want to remove this item?", [field]);

  const handleSaveArrayChange = useCallback(
    (field, index, key, value) => dispatch(storageFieldChange({ clusterName, appId, field, index, key, value })),
    [clusterName, appId, dispatch]
  )

  const handleSaveAddItem = useCallback(
    (field, value) => dispatch(storageFieldIndexAdd({ clusterName, appId, field, value })),
    [clusterName, appId, dispatch]
  )

  const handleDropIndex = useCallback(
    (field, index) => {
      setModalState((prev) => ({ ...prev, field, index, isOpen: true }))
    },
    []
  )

  const handlePauseAutoReload = useCallback(
    () => dispatch(pauseAutoReload({ isPaused: true })),
    [dispatch]
  )

  const handleResumeAutoReload = useCallback(
    () => dispatch(pauseAutoReload({ isPaused: false })),
    [dispatch]
  )

  const actionProps = useMemo(() => ({
    onRowArrayChange: handleSaveArrayChange,
    onSaveAdd: handleSaveAddItem,
    onRowDropIndex: handleDropIndex,
    onPauseAutoReload: handlePauseAutoReload,
    onResumeAutoReload: handleResumeAutoReload,
  }), [handleSaveArrayChange, handleSaveAddItem, handleDropIndex, handlePauseAutoReload, handleResumeAutoReload]);

  const handleCloseConfirm = useCallback(() => {
    setModalState({ isOpen: false, field: null, index: null });
  }, []);

  const handleConfirmDrop = useCallback(() => {
    if (field && typeof index === 'number') {
      dispatch(storageFieldIndexDrop({ clusterName, appId, field, index }));
      handleCloseConfirm();
    }
  }, [clusterName, appId, field, index, dispatch]);

  const gitClones = storages?.gitClones || [];
  const volumes = storages?.volumes || [];
  const s3Directories = storages?.s3Directories || [];

  const volumeOptions = useMemo(() => {
    return volumes.map((vol) => ({ value: vol.name, name: vol.name, volumedir: vol.volumedir }));
  },[volumes])

  const gitComponent = useMemo(() => (
      <GitCloneSection rows={gitClones} volumeOptions={volumeOptions} {...actionProps} />
  ), [gitClones, volumeOptions, actionProps]);

  const volumeComponent = useMemo(() => (
      <VolumeSection
        volumePools={volumePools}
        fieldName="volumes"
        title="Saved Volumes"
        newTitle="Add New Volume"
        addCaption="Add Volume"
        saveCaption="Save Volume"
        rows={volumes}
        {...actionProps}
      />
  ), [volumes, actionProps]);

  const s3Component = useMemo(() => (
      <S3DirectorySection rows={s3Directories} {...actionProps} />
  ), [s3Directories, actionProps]);

  return (
    <Flex direction="column" className={styles.sectionWrapper}>
      <VStack spacing={3} align="stretch">
        <AccordionComponent heading={'Volumes Section'} body={volumeComponent} />
        <AccordionComponent heading={'Git Clones Section'} body={gitComponent} />
        <AccordionComponent heading={'S3 Directories Section'} body={s3Component} />
      </VStack>
      <ConfirmModal
        isOpen={isConfirmOpen}
        closeModal={handleCloseConfirm}
        onConfirmClick={handleConfirmDrop}
        title="Confirm Delete"
        body={dropConfirmText}
      />
    </Flex>
  );
}
