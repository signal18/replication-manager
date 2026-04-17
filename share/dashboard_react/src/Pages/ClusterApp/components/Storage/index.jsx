import { Box, VStack, Heading, Flex } from "@chakra-ui/react";
import GitCloneSection from "./GitClone";
import VolumeSection from "./Volume";
import S3DirectorySection from "./S3Directory";
import styles from "./styles.module.scss";
import { useCallback, useMemo, useState } from "react";
import { useDispatch, useSelector } from "react-redux";
import AccordionComponent from "../../../../components/AccordionComponent";
import ConfirmModal from "../../../../components/Modals/ConfirmModal";
import { addS3Provider, pauseAutoReload, selectClusterS3Providers, storageFieldChange, storageFieldIndexAdd, storageFieldIndexDrop, previewS3MountSync, applyS3MountSync, getClusterData } from "../../../../redux/clusterSlice";

export default function StoragePage({ clusterName, appId, user }) {
  const dispatch = useDispatch();
  const storages = useSelector((state) => state.cluster?.app?.deployment?.storages);
  const volumePools = useSelector((state) => state.cluster?.clusterData?.config?.provAppVolumePools);
  const s3Providers = useSelector((state) => state.cluster?.clusterData?.appS3Providers);
  const clusterS3Providers = useSelector(selectClusterS3Providers);
  const clusterApps = useSelector((state) => state.cluster?.clusterApps || []);

  const getAppEndpoint = useCallback((app) => {
    if (!app) return "";
    if (app.host && app.port) return `${app.host}:${app.port}`;
    if (app.name && app.port) return `${app.name}:${app.port}`;
    return app.id || "";
  }, []);

  const s3ProvOptions = useMemo(() => {
    const providerSet = new Set(s3Providers || []);
    const appOptions = (clusterApps || [])
      .map((app) => {
        const endpoint = getAppEndpoint(app);
        if (!endpoint || !providerSet.has(endpoint)) return null;
        return {
          value: endpoint,
          name: app.id || app.name || endpoint,
          endpoint,
          source: 'app',
        };
      })
      .filter(Boolean);

    const knownValues = new Set(appOptions.map((opt) => opt.value));
    const fallbackOptions = (s3Providers || [])
      .filter((prov) => !knownValues.has(prov))
      .map((prov) => ({
        value: prov,
        name: prov,
        endpoint: prov,
        source: 'app',
      }));

    return [...appOptions, ...fallbackOptions];
  }, [s3Providers, clusterApps, getAppEndpoint]);

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

  const handleSaveAsProvider = useCallback(
    (name, s3, providerSource) => {  // P2: use providerSource from form
      const source = providerSource || "custom";
      const payload = { name, providerSource: source, region: s3.region || "" };
      if (source === "app") {
        payload.providerApp = s3.endpoint || "";
      } else {
        payload.endpoint = s3.endpoint || "";
        payload.accesskey = s3.accesskey || s3.accessKey || "";
        payload.secretkey = s3.secretkey || s3.secretKey || "";
      }
      return dispatch(addS3Provider({ clusterName, payload })).unwrap();
    },
    [clusterName, dispatch]
  );

  const handlePreviewSync = useCallback(
    (providerName, mountName) =>
      dispatch(previewS3MountSync({ clusterName, providerName, appId, mountName })).unwrap(),
    [clusterName, appId, dispatch]
  );

  const handleApplySync = useCallback(
    async (providerName, mountName, revisionToken) => {
      const resp = await dispatch(applyS3MountSync({ clusterName, providerName, appId, mountName, revisionToken })).unwrap();
      const changed = Number(resp?.data?.summary?.changed || 0);
      if (changed > 0) {
        await dispatch(getClusterData({ clusterName }));
      }
      return resp;
    },
    [clusterName, appId, dispatch]
  );

  const actionProps = useMemo(() => ({
    onRowArrayChange: handleSaveArrayChange,
    onSaveAdd: handleSaveAddItem,
    onRowDropIndex: handleDropIndex,
    onPauseAutoReload: handlePauseAutoReload,
    onResumeAutoReload: handleResumeAutoReload,
    onSaveAsProvider: handleSaveAsProvider,
    onPreviewSync: handlePreviewSync,
    onApplySync: handleApplySync,
  }), [handleSaveArrayChange, handleSaveAddItem, handleDropIndex, handlePauseAutoReload, handleResumeAutoReload, handleSaveAsProvider, handlePreviewSync, handleApplySync]);

  const handleCloseConfirm = useCallback(() => {
    setModalState({ isOpen: false, field: null, index: null });
  }, []);

  const handleConfirmDrop = useCallback(() => {
    if (field && typeof index === 'number') {
      dispatch(storageFieldIndexDrop({ clusterName, appId, field, index }));
      handleCloseConfirm();
    }
  }, [clusterName, appId, field, index, dispatch, handleCloseConfirm]);  // P5: add handleCloseConfirm

  const gitClones = storages?.gitClones || [];
  const volumes = storages?.volumes || [];
  const s3Mounts = storages?.s3Mounts || [];

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
      <S3DirectorySection appId={appId} rows={s3Mounts} s3ProvOptions={s3ProvOptions} clusterS3Providers={clusterS3Providers} {...actionProps} />
  ), [appId, s3Mounts, s3ProvOptions, clusterS3Providers, actionProps]);

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
