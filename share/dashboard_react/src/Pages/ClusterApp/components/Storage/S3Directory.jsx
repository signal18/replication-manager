import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { VStack, Input, HStack, Heading, Flex, Box, Text } from "@chakra-ui/react";
import styles from "./styles.module.scss";
import TextForm from "../../../../components/TextForm";
import { createColumnHelper } from "@tanstack/react-table";
import RMButton from "../../../../components/RMButton";
import RMIconButton from "../../../../components/RMIconButton";
import { DataTable } from "../../../../components/DataTable";
import { HiTrash } from "react-icons/hi";
import Dropdown from "../../../../components/Dropdown";
import SyncDiffTable from "../../../../components/SyncDiffTable";
import { extractApiErrorMessage, redactSensitiveInfo } from "../../../../utils/apiError";
import {
  buildVolumeDir,
  defaultS3Subdir,
  extractSubDir,
  getVolumeDirTokens,
  matchVolumeDirToken,
} from "./volumeDirUtils";

const defaultS3 = { name: "", endpoint: "", bucket: "", region: "", accesskey: "", secretkey: "", providerName: "", volumename: "", volumedir: "", subpath: "" };
const providerSourceOptions = [
  { value: "app", name: "Sibling App" },
  { value: "custom", name: "Custom Endpoint" },
];

const endpointExistsInProviders = (endpoint, s3ProvOptions = []) =>
  !!endpoint && s3ProvOptions.some((opt) => opt.value === endpoint || opt.endpoint === endpoint);

const getDuplicateProviderAdvisory = (name, clusterS3Providers = []) => {
  const trimmedName = name.trim();
  if (!trimmedName) {
    return "";
  }
  const hasDuplicate = (clusterS3Providers || []).some((provider) => provider.name === trimmedName);
  return hasDuplicate
    ? `A provider named "${trimmedName}" already exists in your current snapshot. You can still submit to confirm with the server.`
    : "";
};

const hasCustomBlankCredentials = (providerSource, s3 = {}) => {
  if (providerSource !== "custom") return false;
  return !s3.accesskey && !s3.secretkey;
};

const columnHelper = createColumnHelper()

const validateSingleSyncResult = (resp, expectedProviderName, expectedMountName, expectedAppId, allowedStatuses = []) => {
  const data = resp?.data;
  if (!data || typeof data !== "object" || Array.isArray(data)) {
    return { ok: false, error: "Invalid response from server." };
  }
  if (data.providerName !== expectedProviderName) {
    return { ok: false, error: "Invalid response from server: provider mismatch." };
  }
  const results = Array.isArray(data.results) ? data.results : null;
  if (!results || results.length !== 1) {
    return { ok: false, error: "Invalid response from server: expected one target result." };
  }
  const result = results[0];
  const targetMount = result?.target?.mountName;
  if (targetMount !== expectedMountName) {
    return { ok: false, error: "Invalid response from server: target mismatch." };
  }
  const targetAppId = String(result?.target?.appId || '');
  if (expectedAppId && targetAppId !== expectedAppId) {
    return { ok: false, error: "Invalid response from server: app target mismatch." };
  }
  if (!allowedStatuses.includes(result?.status)) {
    return { ok: false, error: "Invalid response from server: unknown sync status." };
  }
  return { ok: true, result, data };
};

const S3DirectorySection = ({
    rows = [],
    appId = "",
    fieldName = "s3Mounts",
    volumeOptions = [],
    s3ProvOptions = [],
    clusterS3Providers = [],
    onRowArrayChange,
    onRowDropIndex,
    onSaveAdd,
    onSaveAsProvider = () => Promise.resolve(),
    onPauseAutoReload = () => { },
    onResumeAutoReload = () => { },
    onPreviewSync = () => Promise.resolve(),
    onApplySync = () => Promise.resolve(),
}) => {
    const [isVisible, setIsVisible] = useState(false);

    const handleAddItem = () => {
        setIsVisible(true);
        onPauseAutoReload();
    };

    const handleCancel = () => {
        setIsVisible(false);
        onResumeAutoReload();
    };

    const handleSaveAdd = (formData) => {
      onSaveAdd(fieldName, formData).then(() => {
        setIsVisible(false);
        onResumeAutoReload();
      }).catch(() => {
        // Error banner shown by storageFieldIndexAdd in redux. Resume auto-reload so UI doesn't stay paused.
        onResumeAutoReload();
      });
    }

    const columnsRowForm = useMemo(
        () => [
            columnHelper.accessor((row) => row.name, {
                header: 'Name'
            }),
            columnHelper.accessor((row) => row.endpoint, {
                header: 'Endpoint'
            }),
            columnHelper.accessor((row) => row.bucket, {
                header: 'Bucket',
            }),
            columnHelper.display({
                id: 'actions',
                cell: ({ row }) => (
                    <RMIconButton
                        icon={HiTrash}
                        aria-label="Delete Variable"
                        onClick={() => onRowDropIndex(fieldName, row.index)}
                    />
                ),
            }),
            {
                id: 'expansion',
                header: '',
                meta: {
                    renderExpansion: (row) => {
                        return (<S3DirectoryRowForm appId={appId} fieldName={fieldName} volumeOptions={volumeOptions} s3ProvOptions={s3ProvOptions} clusterS3Providers={clusterS3Providers} s3directory={row.original} index={row.index} onChange={onRowArrayChange} onSaveAsProvider={onSaveAsProvider} onPreviewSync={onPreviewSync} onApplySync={onApplySync} />);
                    },
                },
                cell: () => null,
            }
        ],
        [appId, fieldName, volumeOptions, onRowArrayChange, onRowDropIndex, s3ProvOptions, clusterS3Providers, onSaveAsProvider, onPreviewSync, onApplySync]
    )

    return (
        <Flex direction="column" className={`${styles.sectionWrapper}`}>
            <VStack spacing={3} align="stretch">
                <Heading as="h3" size="md">
                    Saved S3 Directories
                </Heading>
                <Box className={styles.tableContainer}>
                    <DataTable key="app-variables" data={rows} columns={columnsRowForm} className={styles.table} enableExpanding={true} enableExpandingNoSubRows={true} />
                </Box>
            </VStack>
            {isVisible ? (
                <VStack spacing={3} align="stretch">
                    <Heading as="h3" size="md">
                        Add New S3 Directory
                    </Heading>
                    <Box className={styles.tableContainer}>
                        <S3DirectoryNewForm volumeOptions={volumeOptions} s3ProvOptions={s3ProvOptions} clusterS3Providers={clusterS3Providers} onSave={handleSaveAdd} onCancel={handleCancel} onSaveAsProvider={onSaveAsProvider} />
                    </Box>
                </VStack>
            ) : (
                <VStack spacing={3} align="stretch">
                    <HStack>
                        <RMButton onClick={handleAddItem}>
                            Add S3 Directory
                        </RMButton>
                    </HStack>
                </VStack>
            )}
        </Flex>
    )
}

export default React.memo(S3DirectorySection);

const S3DirectoryRowForm = React.memo(({ appId = "", fieldName, volumeOptions = [], s3ProvOptions, clusterS3Providers = [], s3directory, index, onChange, onSaveAsProvider = () => Promise.resolve(), onPreviewSync = () => Promise.resolve(), onApplySync = () => Promise.resolve() }) => {
    const s3 = s3directory || defaultS3;
    const { volumename, volumedir } = s3;
    const [providerSource, setProviderSource] = useState(() =>
      endpointExistsInProviders(s3.endpoint, s3ProvOptions) ? "app" : "custom"
    );
    const [showSaveProviderUI, setShowSaveProviderUI] = useState(false);
    const [providerName, setProviderName] = useState("");
    const [saveProviderError, setSaveProviderError] = useState("");
    const [isSubmitting, setIsSubmitting] = useState(false);
    const duplicateProviderAdvisory = useMemo(
      () => getDuplicateProviderAdvisory(providerName, clusterS3Providers),
      [providerName, clusterS3Providers]
    );

    // Sync state: 'idle' | 'loading' | 'preview' | 'applying' | 'done' | 'error'
    const [syncState, setSyncState] = useState('idle');
    const [syncPreview, setSyncPreview] = useState(null);   // SyncPreviewResult for this mount
    const [syncRevisionToken, setSyncRevisionToken] = useState('');
    const [syncApplyResult, setSyncApplyResult] = useState(null);
    const [syncError, setSyncError] = useState("");
    const syncRequestIdRef = useRef(0);

    const nextSyncRequestId = () => {
      syncRequestIdRef.current += 1;
      return syncRequestIdRef.current;
    };

    useEffect(() => {
      // Any identity change invalidates in-flight sync callbacks and stale snapshots.
      syncRequestIdRef.current += 1;
      setSyncState('idle');
      setSyncPreview(null);
      setSyncRevisionToken('');
      setSyncApplyResult(null);
      setSyncError("");
    }, [appId, index, s3.name, s3.providerName]);

    const savedProviderOptions = useMemo(() =>
      (clusterS3Providers || [])
        .map((p) => ({ value: p.name, name: p.name })),
      [clusterS3Providers]
    );

    const selectedProviderMissing = useMemo(
      () => !!s3.providerName && !(clusterS3Providers || []).some((p) => p.name === s3.providerName),
      [s3.providerName, clusterS3Providers]
    );

    // Phase 14: explicit V2 S3 mount placement - selected saved volume row,
    // selected directory token within that row, and selected relative
    // subdirectory beneath that token.
    const vol = useMemo(() => volumeOptions.find((opt) => opt.value === volumename), [volumeOptions, volumename]);
    const availableDirs = useMemo(() => getVolumeDirTokens(vol?.volumedir), [vol]);
    const srcbasepath = useMemo(() => matchVolumeDirToken(volumedir, vol?.volumedir, defaultS3Subdir), [volumedir, vol]);
    const subpath = useMemo(() => extractSubDir(volumedir, srcbasepath), [volumedir, srcbasepath]);

    const handleVolume = useCallback((value) => {
      const newVol = volumeOptions.find((opt) => opt.value === value);
      const newDirs = getVolumeDirTokens(newVol?.volumedir);
      const newBase = newDirs.includes(srcbasepath) ? srcbasepath : defaultS3Subdir(newVol?.volumedir);
      const newValue = newVol ? buildVolumeDir(newBase, subpath, s3.name) : volumedir;
      onChange(fieldName, index, "volumename", newVol ? newVol.value : "");
      onChange(fieldName, index, "volumedir", newValue);
    }, [fieldName, index, volumeOptions, srcbasepath, subpath, s3.name, volumedir, onChange]);

    const handleBaseDir = useCallback((value) => {
      onChange(fieldName, index, "volumedir", buildVolumeDir(value, subpath, s3.name));
    }, [fieldName, index, subpath, s3.name, onChange]);

    const handleSubPath = useCallback((value) => {
      onChange(fieldName, index, "volumedir", buildVolumeDir(srcbasepath, value, s3.name));
    }, [fieldName, index, srcbasepath, s3.name, onChange]);

    const handleProviderSourceChange = (option) => {
      const nextSource = option?.value || option;
      setProviderSource(nextSource);
      if (nextSource === "app") {
        if (!endpointExistsInProviders(s3.endpoint, s3ProvOptions) && s3ProvOptions?.length > 0) {
          onChange(fieldName, index, "endpoint", s3ProvOptions[0].value);
        }
        onChange(fieldName, index, "accesskey", "");
        onChange(fieldName, index, "secretkey", "");
      }
    };

    const handleSavedProviderChange = (option) => {
      const name = option?.value || option;
      if (!name) return;
      const provider = (clusterS3Providers || []).find((p) => p.name === name);
      if (!provider) return;
      const endpoint = provider.endpoint || provider.providerApp || "";
      onChange(fieldName, index, "endpoint", endpoint);
      if (provider.region) {
        onChange(fieldName, index, "region", provider.region);
      }
      onChange(fieldName, index, "providerName", name);
      // Clear local credentials when switching to a saved provider to avoid
      // stale secret carryover from previous local custom values.
      onChange(fieldName, index, "accesskey", "");
      onChange(fieldName, index, "secretkey", "");
      // Sync providerSource to match the selected provider's type so the endpoint
      // UI (app dropdown vs text input) stays consistent with the actual value.
      if (provider.providerSource) {
        setProviderSource(provider.providerSource);
      }
    };

    const handleSaveAsProvider = () => {
      if (!providerName.trim()) {
        setSaveProviderError("Provider name is required.");
        return;
      }
      if (!s3.endpoint) {
        setSaveProviderError("Endpoint is required before saving as a provider.");
        return;
      }
      setIsSubmitting(true);
      onSaveAsProvider(providerName.trim(), s3, providerSource)
        .then(() => {
          setShowSaveProviderUI(false);
          setProviderName("");
          setSaveProviderError("");
        })
        .catch((err) => {
          setSaveProviderError(extractApiErrorMessage(err, "Failed to save provider."));
        })
        .finally(() => setIsSubmitting(false));
    };

    const handleSyncPreview = () => {
      if (!s3.providerName) return;
      const requestId = nextSyncRequestId();
      setSyncState('loading');
      setSyncPreview(null);
      setSyncRevisionToken('');
      setSyncApplyResult(null);
      setSyncError("");
      onPreviewSync(s3.providerName, s3.name)
        .then((resp) => {
          if (requestId !== syncRequestIdRef.current) return;
          const validated = validateSingleSyncResult(resp, s3.providerName, s3.name, appId, ['will_change', 'no_change', 'provider_missing', 'error']);
          if (!validated.ok) {
            setSyncError(validated.error);
            setSyncState('error');
            return;
          }
          const revisionToken = typeof validated?.data?.revisionToken === 'string' ? validated.data.revisionToken.trim() : '';
          if (!revisionToken) {
            setSyncError('Invalid response from server: missing preview revision token.');
            setSyncState('error');
            return;
          }
          setSyncPreview(validated.result);
          setSyncRevisionToken(revisionToken);
          setSyncState('preview');
        })
        .catch((err) => {
          if (requestId !== syncRequestIdRef.current) return;
          setSyncError(extractApiErrorMessage(err, "Preview failed."));
          setSyncState('error');
        });
    };

    const handleSyncApply = () => {
      if (!s3.providerName) return;
      if (!syncRevisionToken) {
        setSyncError('Sync preview is missing a revision token. Please run preview again.');
        setSyncState('error');
        return;
      }
      const requestId = nextSyncRequestId();
      setSyncState('applying');
      onApplySync(s3.providerName, s3.name, syncRevisionToken)
        .then((resp) => {
          if (requestId !== syncRequestIdRef.current) return;
          const validated = validateSingleSyncResult(resp, s3.providerName, s3.name, appId, ['changed', 'unchanged', 'provider_missing', 'error', 'stale_state']);
          if (!validated.ok) {
            setSyncError(validated.error);
            setSyncState('error');
            return;
          }
          if (validated.result.status === 'stale_state') {
            setSyncPreview(null);
            setSyncRevisionToken('');
            setSyncApplyResult(null);
            setSyncError(validated.result.errorMessage || 'Preview is stale. Please run preview again before applying.');
            setSyncState('error');
            return;
          }
          setSyncApplyResult(validated.result);
          setSyncState('done');
        })
        .catch((err) => {
          if (requestId !== syncRequestIdRef.current) return;
          setSyncError(extractApiErrorMessage(err, "Apply failed."));
          setSyncState('error');
        });
    };

    const handleSyncReset = () => {
      // Invalidate in-flight preview/apply callbacks.
      syncRequestIdRef.current += 1;
      setSyncState('idle');
      setSyncPreview(null);
      setSyncRevisionToken('');
      setSyncApplyResult(null);
      setSyncError("");
    };

    return (
        <Flex className={styles.variableRowForm} w="100%" align="flex-start" gap={4}>
            <Flex direction="column" flex="1" minW="300px" gap={2}>
                {savedProviderOptions.length > 0 && (
                  <Flex direction="column" flex="1">
                    <Text mb={1}>Saved Provider (optional):</Text>
                    <Dropdown
                      placeholder="Select saved provider to copy settings"
                      selectedValue={s3.providerName || ""}
                      onChange={(option) => handleSavedProviderChange(option)}
                      options={savedProviderOptions}
                    />
                  </Flex>
                )}
                {!!s3.providerName && (
                  <Flex direction="column" flex="1">
                    <Text mb={1}>Provider Trace:</Text>
                    <Text fontSize="sm" color="gray.500">
                      {selectedProviderMissing
                        ? `Copied from provider "${s3.providerName}" (no longer in provider library). Values remain editable locally.`
                        : `Values copied from provider "${s3.providerName}" — editable locally.`}
                    </Text>
                  </Flex>
                )}
                <Flex direction="column" flex="1">
                    <Text mb={1}>Provider Source:</Text>
                    <Dropdown
                      placeholder="Select Provider Source"
                      selectedValue={providerSource}
                      onChange={(option) => handleProviderSourceChange(option)}
                      options={providerSourceOptions}
                    />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>{providerSource === "app" ? "S3 Provider App:" : "Endpoint:"}</Text>
                    {providerSource === "app" ? (
                      <Dropdown
                        confirmTitle={"Confirm provider app change"}
                        placeholder="S3 Provider App"
                        selectedValue={s3.endpoint}
                        onChange={(value) => onChange(fieldName, index, "endpoint", value)}
                        options={s3ProvOptions}
                      />
                    ) : (
                      <TextForm
                        placeholder="host:port or https://endpoint"
                        value={s3.endpoint}
                        onSave={(value) => onChange(fieldName, index, "endpoint", value)}
                      />
                    )}
                    <Text mt={1} fontSize="sm" color="gray.500">
                      {providerSource === "app"
                        ? "Choose a sibling app configured as an S3 provider."
                        : "Define a custom endpoint (must be reachable and properly configured)."}
                    </Text>
                    {hasCustomBlankCredentials(providerSource, s3) && (
                      <Text mt={1} fontSize="sm" color="orange.400">
                        Warning: custom endpoint is using blank credentials. This is only valid for public buckets.
                      </Text>
                    )}
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Bucket:</Text>
                    <TextForm placeholder="Bucket" value={s3.bucket} onSave={(value) => onChange(fieldName, index, "bucket", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume:</Text>
                    <Dropdown key={`s3-volume-${index}`} placeholder="Volume" confirmTitle="Change Volume" options={volumeOptions} selectedValue={volumename} onChange={(value) => handleVolume(value)} />
                </Flex>
                {availableDirs.length > 1 ? (
                    <Flex direction="column" flex="1">
                        <Text mb={1}>Volume Dir:</Text>
                        <Dropdown
                          key={`s3-basedir-${index}`}
                          placeholder="Volume Dir"
                          confirmTitle="Change Volume Dir"
                          options={availableDirs.map((dir) => ({ value: dir, name: dir }))}
                          selectedValue={srcbasepath}
                          onChange={(value) => handleBaseDir(value)}
                        />
                    </Flex>
                ) : (
                    srcbasepath && (<Text key={`${volumename}-basepath`} mb={1} fontSize="sm" color="gray.500">Basepath: {srcbasepath}</Text>)
                )}
                <Flex direction="column" flex="1">
                    <Text mb={1}>Sub Dir:</Text>
                    <TextForm key={`s3-subdir-${index}`} placeholder="Volume Sub Dir" confirmTitle="Change Volume Sub Dir" value={subpath} onSave={(value) => handleSubPath(value)} />
                    <Text>Fullpath: {volumedir || "(auto-assigned on save)"}</Text>
                </Flex>
                <Flex direction="column" flex="1" gap={1}>
                    <Text
                      as="button"
                      color="blue.400"
                      cursor="pointer"
                      fontSize="sm"
                      textAlign="left"
                      onClick={() => { if (!isSubmitting) { setShowSaveProviderUI((v) => !v); setSaveProviderError(""); } }}
                    >
                      Or save this mount as a new provider
                    </Text>
                    {showSaveProviderUI && (
                      <Flex direction="column" gap={1} mt={1}>
                        <Input
                          placeholder="Provider name"
                          value={providerName}
                          onChange={(e) => { setProviderName(e.target.value); setSaveProviderError(""); }}
                        />
                        {saveProviderError && (
                          <Text color="red.400" fontSize="sm">{saveProviderError}</Text>
                        )}
                        {!saveProviderError && duplicateProviderAdvisory && (
                          <Text color="orange.400" fontSize="sm">{duplicateProviderAdvisory}</Text>
                        )}
                        <RMButton onClick={handleSaveAsProvider} isDisabled={isSubmitting}>
                          {isSubmitting ? "Saving…" : "Save as Provider"}
                        </RMButton>
                      </Flex>
                    )}
                </Flex>
                {!!s3.providerName && (
                  <Flex direction="column" flex="1" gap={1}>
                    <Text
                      as="button"
                      color="teal.400"
                      cursor="pointer"
                      fontSize="sm"
                      textAlign="left"
                      onClick={() => { if (syncState === 'idle') { handleSyncPreview(); } else { handleSyncReset(); } }}
                    >
                      {syncState === 'idle' ? 'Sync from provider' : 'Reset sync'}
                    </Text>
                    {syncState === 'loading' && (
                      <Text fontSize="sm" color="gray.500">Loading preview…</Text>
                    )}
                    {syncState === 'applying' && (
                      <Text fontSize="sm" color="gray.500">Applying sync…</Text>
                    )}
                    {syncState === 'error' && (
                      <Text fontSize="sm" color="red.400">{syncError}</Text>
                    )}
                    {syncState === 'preview' && syncPreview && (
                      <Flex direction="column" gap={1} mt={1} p={2} borderWidth="1px" borderRadius="md" borderColor="teal.200">
                        <SyncDiffTable results={[syncPreview]} />
                        {syncPreview.status === 'will_change' && (
                          <RMButton mt={2} onClick={handleSyncApply}>Apply Sync</RMButton>
                        )}
                        {syncPreview.status === 'no_change' && (
                          <RMButton mt={2} isDisabled>
                            Apply Sync (not needed)
                          </RMButton>
                        )}
                      </Flex>
                    )}
                    {syncState === 'done' && syncApplyResult && (
                      <Flex direction="column" gap={1} mt={1} p={2} borderWidth="1px" borderRadius="md" borderColor="green.200">
                        {syncApplyResult.status === 'changed' && (
                          <>
                            <Text fontSize="sm" color="green.500">
                              Sync applied. Updated: {(syncApplyResult.changesApplied || []).join(', ')}.
                            </Text>
                            {!!syncApplyResult.errorMessage && (
                              <Text fontSize="sm" color="orange.500">Warning: {syncApplyResult.errorMessage}</Text>
                            )}
                          </>
                        )}
                        {syncApplyResult.status === 'unchanged' && (
                          <Text fontSize="sm" color="green.500">Mount already matches provider — no changes needed.</Text>
                        )}
                        {syncApplyResult.status === 'provider_missing' && (
                          <Text fontSize="sm" color="red.400">Provider not found. Cannot apply sync.</Text>
                        )}
                        {syncApplyResult.status === 'error' && (
                          <Text fontSize="sm" color="red.400">Sync error: {redactSensitiveInfo(syncApplyResult.errorMessage || 'Apply failed.')}</Text>
                        )}
                      </Flex>
                    )}
                  </Flex>
                )}
            </Flex>
        </Flex>
    )
})

const S3DirectoryNewForm = React.memo(({ s3ProvOptions = [], clusterS3Providers = [], volumeOptions = [], onSave = () => { }, onCancel = () => { }, onSaveAsProvider = () => Promise.resolve() }) => {
    const [s3, setS3] = useState(defaultS3);
    const [providerSource, setProviderSource] = useState(s3ProvOptions?.length ? "app" : "custom");
    const [showSaveProviderUI, setShowSaveProviderUI] = useState(false);
    const [providerName, setProviderName] = useState("");
    const [saveProviderError, setSaveProviderError] = useState("");
    const [isSubmitting, setIsSubmitting] = useState(false);
    const { volumename, volumedir, subpath } = s3;
    const duplicateProviderAdvisory = useMemo(
      () => getDuplicateProviderAdvisory(providerName, clusterS3Providers),
      [providerName, clusterS3Providers]
    );

    // Phase 14: explicit V2 S3 mount placement for the "Add new" form.
    const vol = useMemo(() => volumeOptions.find((opt) => opt.value === volumename), [volumeOptions, volumename]);
    const availableDirs = useMemo(() => getVolumeDirTokens(vol?.volumedir), [vol]);
    const srcbasepath = useMemo(() => matchVolumeDirToken(volumedir, vol?.volumedir, defaultS3Subdir), [volumedir, vol]);

    // Phase 16: a bare directory-token volumedir (no Sub Dir typed yet) gets
    // the generated mount name appended server-side, so preview that here
    // rather than showing the bare token as if it were the final path.
    const fullpathPreview = useMemo(() => {
      if (!volumedir) return "(auto-assigned on save)";
      const trimmedSub = typeof subpath === "string" ? subpath.trim() : "";
      if (!trimmedSub || trimmedSub === "/") return `${volumedir}/<name auto-assigned on save>`;
      return volumedir;
    }, [volumedir, subpath]);

    const valid = useMemo(() => {
        return s3.endpoint && s3.bucket;
    }, [s3.endpoint, s3.bucket]);

    const savedProviderOptions = useMemo(() =>
      (clusterS3Providers || [])
        .map((p) => ({ value: p.name, name: p.name })),
      [clusterS3Providers]
    );

    const selectedProviderMissing = useMemo(
      () => !!s3.providerName && !(clusterS3Providers || []).some((p) => p.name === s3.providerName),
      [s3.providerName, clusterS3Providers]
    );

    const handleArrayChange = (key, value) => {
        setS3((prev) => ({ ...prev, [key]: value }));
    }

    const handleVolume = (option) => {
        const newDirs = getVolumeDirTokens(option?.volumedir);
        const newBase = newDirs.includes(srcbasepath) ? srcbasepath : defaultS3Subdir(option?.volumedir);
        setS3((prev) => ({
            ...prev,
            volumename: option?.value || "",
            volumedir: buildVolumeDir(newBase, subpath, "", { preserveBareToken: true }),
        }));
    };

    const handleBaseDir = (option) => {
        handleArrayChange("volumedir", buildVolumeDir(option?.value, subpath, "", { preserveBareToken: true }));
    };

    const handleSubPath = (value) => {
        setS3((prev) => ({
            ...prev,
            subpath: value,
            volumedir: buildVolumeDir(srcbasepath, value, "", { preserveBareToken: true }),
        }));
    };

    const handleProviderSourceChange = (option) => {
      const nextSource = option?.value || option;
      setProviderSource(nextSource);
      if (nextSource === "app") {
        const firstEndpoint = s3ProvOptions?.[0]?.value || "";
        setS3((prev) => ({ ...prev, endpoint: firstEndpoint, accesskey: "", secretkey: "" }));
      }
    }

    const handleSavedProviderChange = (option) => {
      const name = option?.value || option;
      if (!name) return;
      const provider = (clusterS3Providers || []).find((p) => p.name === name);
      if (!provider) return;
      const endpoint = provider.endpoint || provider.providerApp || "";
      setS3((prev) => ({
        ...prev,
        endpoint,
        region: provider.region || prev.region,
        accesskey: "",
        secretkey: "",
        providerName: name,
      }));
      // Sync providerSource to match the selected provider's type so the endpoint
      // UI (app dropdown vs text input) stays consistent with the actual value.
      if (provider.providerSource) {
        setProviderSource(provider.providerSource);
      }
    };

    const handleSaveAdd = () => {
        if (valid) {
            onSave(s3)
        }
    };

    const handleCancel = () => {
        setS3(defaultS3);
        setProviderSource(s3ProvOptions?.length ? "app" : "custom");
        onCancel();
    };

    const handleSaveAsProvider = () => {
      if (!providerName.trim()) {
        setSaveProviderError("Provider name is required.");
        return;
      }
      if (!s3.endpoint) {
        setSaveProviderError("Endpoint is required before saving as a provider.");
        return;
      }
      setIsSubmitting(true);
      onSaveAsProvider(providerName.trim(), s3, providerSource)
        .then(() => {
          setShowSaveProviderUI(false);
          setProviderName("");
          setSaveProviderError("");
        })
        .catch((err) => {
          setSaveProviderError(extractApiErrorMessage(err, "Failed to save provider."));
        })
        .finally(() => setIsSubmitting(false));
    };

    return (
        <Flex className={styles.S3DirectoryRowForm} w="100%" align="flex-start" gap={4}>
            <Flex direction="column" flex="1" minW="300px" gap={2}>
                {savedProviderOptions.length > 0 && (
                  <Flex direction="column" flex="1">
                    <Text mb={1}>Saved Provider (optional):</Text>
                    <Dropdown
                      placeholder="Select saved provider to copy settings"
                      selectedValue={s3.providerName || ""}
                      onChange={(option) => handleSavedProviderChange(option)}
                      options={savedProviderOptions}
                    />
                  </Flex>
                )}
                {!!s3.providerName && (
                  <Flex direction="column" flex="1">
                    <Text mb={1}>Provider Trace:</Text>
                    <Text fontSize="sm" color="gray.500">
                      {selectedProviderMissing
                        ? `Copied from provider "${s3.providerName}" (no longer in provider library). Values remain editable locally.`
                        : `Values copied from provider "${s3.providerName}" — editable locally.`}
                    </Text>
                  </Flex>
                )}
                <Flex direction="column" flex="1">
                    <Text mb={1}>Provider Source:</Text>
                    <Dropdown placeholder="Select Provider Source" selectedValue={providerSource} onChange={(option) => handleProviderSourceChange(option)} options={providerSourceOptions} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>{providerSource === "app" ? "S3 Provider App:" : "Endpoint:"}</Text>
                    {providerSource === "app" ? (
                      <Dropdown placeholder="S3 Provider App" selectedValue={s3.endpoint} onChange={(option) => handleArrayChange("endpoint", option.value)} options={s3ProvOptions} />
                    ) : (
                      <Input placeholder="host:port or https://endpoint" value={s3.endpoint} onChange={(e) => handleArrayChange("endpoint", e.target.value)} />
                    )}
                    <Text mt={1} fontSize="sm" color="gray.500">
                      {providerSource === "app"
                        ? "Choose a sibling app configured as an S3 provider."
                        : "Define a custom endpoint (must be reachable and properly configured)."}
                    </Text>
                    {hasCustomBlankCredentials(providerSource, s3) && (
                      <Text mt={1} fontSize="sm" color="orange.400">
                        Warning: custom endpoint is using blank credentials. This is only valid for public buckets.
                      </Text>
                    )}
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Bucket:</Text>
                    <Input placeholder="Bucket Name" value={s3.bucket} onChange={(e) => handleArrayChange("bucket", e.target.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Region:</Text>
                    <Input placeholder="e.g. us-east-1" value={s3.region} onChange={(e) => handleArrayChange("region", e.target.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume:</Text>
                    <Dropdown key="s3-volume-new" placeholder="Volume" options={volumeOptions} selectedValue={volumename} onChange={(option) => handleVolume(option)} />
                </Flex>
                {availableDirs.length > 1 ? (
                    <Flex direction="column" flex="1">
                        <Text mb={1}>Volume Dir:</Text>
                        <Dropdown
                          key="s3-basedir-new"
                          placeholder="Volume Dir"
                          options={availableDirs.map((dir) => ({ value: dir, name: dir }))}
                          selectedValue={srcbasepath}
                          onChange={(option) => handleBaseDir(option)}
                        />
                    </Flex>
                ) : (
                    srcbasepath && (<Text key={`${volumename}-basepath`} mb={1} fontSize="sm" color="gray.500">Basepath: {srcbasepath}</Text>)
                )}
                <Flex direction="column" flex="1">
                    <Text mb={1}>Sub Dir:</Text>
                    <Input key="s3-subdir-new" placeholder="Volume Sub Dir" value={subpath} onChange={(e) => handleSubPath(e.target.value)} />
                    <Text>Fullpath: {fullpathPreview}</Text>
                </Flex>
                {providerSource === "custom" && (
                  <>
                    <Flex direction="column" flex="1">
                        <Text mb={1}>Access Key:</Text>
                        <Input placeholder="Access Key ID" value={s3.accesskey} onChange={(e) => handleArrayChange("accesskey", e.target.value)} />
                    </Flex>
                    <Flex direction="column" flex="1">
                        <Text mb={1}>Secret Key:</Text>
                        <Input type="password" placeholder="Secret Access Key" value={s3.secretkey} onChange={(e) => handleArrayChange("secretkey", e.target.value)} />
                    </Flex>
                  </>
                )}
                <Flex direction="column" flex="1">
                    <HStack spacing={2} mt={4}>
                        <RMButton onClick={handleCancel}>
                            Clear Form
                        </RMButton>
                        <RMButton onClick={handleSaveAdd} isDisabled={!valid}>
                            Save S3 Directory
                        </RMButton>
                        <RMButton onClick={() => { if (!isSubmitting) { setShowSaveProviderUI((v) => !v); setSaveProviderError(""); } }} isDisabled={isSubmitting}>
                            Save as Provider
                        </RMButton>
                    </HStack>
                    {showSaveProviderUI && (
                      <Flex direction="column" gap={1} mt={2}>
                        <Input
                          placeholder="Provider name"
                          value={providerName}
                          onChange={(e) => { setProviderName(e.target.value); setSaveProviderError(""); }}
                        />
                        {saveProviderError && (
                          <Text color="red.400" fontSize="sm">{saveProviderError}</Text>
                        )}
                        {!saveProviderError && duplicateProviderAdvisory && (
                          <Text color="orange.400" fontSize="sm">{duplicateProviderAdvisory}</Text>
                        )}
                        <RMButton onClick={handleSaveAsProvider} isDisabled={isSubmitting}>
                          {isSubmitting ? "Saving…" : "Confirm Save as Provider"}
                        </RMButton>
                      </Flex>
                    )}
                </Flex>
            </Flex>
        </Flex>
    )
})
