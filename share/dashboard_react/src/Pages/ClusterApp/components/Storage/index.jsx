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

  const [modalState, setModalState] = useState({
    isOpen: false,
    field: null,
    index: null,
  })

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
  const localDirectories = storages?.localDirectories || [];
  const sharedDirectories = storages?.sharedDirectories || [];
  const s3Directories = storages?.s3Directories || [];

  const gitComponent = useMemo(() => (
      <GitCloneSection rows={gitClones} {...actionProps} />
  ), [gitClones, actionProps]);

  const localComponent = useMemo(() => (
      <VolumeSection
        fieldName="localDirectories"
        title="Saved Local Directories"
        newTitle="Add New Local Directory"
        addCaption="Add Local Directory"
        saveCaption="Save Local Directory"
        rows={localDirectories}
        {...actionProps}
      />
  ), [localDirectories, actionProps]);

  const sharedComponent = useMemo(() => (
      <VolumeSection
        fieldName="sharedDirectories"
        title="Saved Shared Directories"
        newTitle="Add New Shared Directory"
        addCaption="Add Shared Directory"
        saveCaption="Save Shared Directory"
        rows={sharedDirectories}
        {...actionProps}
      />
  ), [sharedDirectories, actionProps]);

  const s3Component = useMemo(() => (
      <S3DirectorySection rows={s3Directories} {...actionProps} />
  ), [s3Directories, actionProps]);

  return (
    <Flex direction="column" className={styles.sectionWrapper}>
      <VStack spacing={3} align="stretch">
        <AccordionComponent heading={'Git Clones Section'} body={gitComponent} />
        <AccordionComponent heading={'Local Directories Section'} body={localComponent} />
        <AccordionComponent heading={'Shared Directories Section'} body={sharedComponent} />
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
