import PropTypes from "prop-types";
import { useState, useCallback, useMemo } from "react";
import {
  VStack, HStack, Input, Select, Checkbox, Heading, Badge,
  Alert, AlertIcon, AlertTitle, AlertDescription, Flex, Box, Text,
  FormControl, FormLabel, FormHelperText, Divider, Tooltip,
  InputGroup, InputRightElement,
} from "@chakra-ui/react";
import { useDispatch, useSelector } from "react-redux";
import { createColumnHelper } from "@tanstack/react-table";
import { HiTrash, HiFolder, HiQuestionMarkCircle } from "react-icons/hi";
import { TbEdit } from "react-icons/tb";
import AccordionComponent from "../../../../components/AccordionComponent";
import { DataTable } from "../../../../components/DataTable";
import NumberInput from "../../../../components/NumberInput";
import RMButton from "../../../../components/RMButton";
import RMIconButton from "../../../../components/RMIconButton";
import ConfirmModal from "../../../../components/Modals/ConfirmModal";
import CommonModal from "../../../../components/Modals/CommonModal";
import TreeView from "../../../../components/Modals/TreeView/TreeView";
import modalStyles from "../../../../components/Modals/styles.module.scss";
import {
  addCanonicalVolume, updateCanonicalVolume, dropCanonicalVolume,
  addCanonicalSource, updateCanonicalSource, dropCanonicalSource,
  addCanonicalMount, updateCanonicalMount, dropCanonicalMount,
  selectClusterS3Providers,
} from "../../../../redux/clusterSlice";
import { checkGitRepo, getDockerTree } from "../../../../redux/pathSlice";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import styles from "./styles.module.scss";

const columnHelper = createColumnHelper();
const IDLE = "idle";
const EDIT = "edit";
const ADD = "add";
const PER_VOLUME_STRATEGY = "per-volume";
const DEFAULT_CREDIT_VOLUME_SIZE_GIB = 10;

const nodeToValue = (node) => (node.type === "directory" && !node.path.endsWith("/") ? node.path + "/" : node.path);
const nodeToString = (node) => node.name || node.path;

const getRuntimeVolumeNameTemplate = (vol, physicalVolumeStrategy) => {
  if (vol.runtimeName) return vol.runtimeName;
  if (physicalVolumeStrategy === "legacy-pooled") return `{name}-${vol.pool}`;
  return `{name}-vol-${vol.name}`;
};

const getDefaultVol = (sizeGiB) => ({ name: "", pool: "", size: `${sizeGiB}g`, shared: false });

const normalizeGiB = (value) => {
  if (!Number.isFinite(value)) {
    return Number.NaN;
  }
  return Math.round(value * 1000) / 1000;
};

const sizeToGiB = (value) => {
  if (value === undefined || value === null || value === "") {
    return Number.NaN;
  }

  const match = String(value).trim().match(/^([\d.]+)\s*([KMGTPE]?)(B)?$/i);
  if (!match) {
    return Number.NaN;
  }

  const number = Number(match[1]);
  if (!Number.isFinite(number)) {
    return Number.NaN;
  }

  const unit = (match[2] || "G").toUpperCase();
  const multipliers = {
    G: 1,
    T: 1000,
    P: 1000 ** 2,
    E: 1000 ** 3,
  };

  const factor = multipliers[unit];
  if (!factor) {
    return Number.NaN;
  }

  return normalizeGiB(number * factor);
};

const sumVolumeSizesGiB = (rows = []) => rows.reduce((total, row) => {
  const sizeGiB = sizeToGiB(row?.size);
  return Number.isFinite(sizeGiB) ? total + sizeGiB : total;
}, 0);

const isStandardCreditSized = (sizeGiB, creditUnitGiB) => Number.isFinite(sizeGiB) && sizeGiB >= creditUnitGiB && Number.isInteger(sizeGiB / creditUnitGiB);

const hVolumeName = `**Canonical Name**\n\nStable logical identity for this canonical volume. Sources and mounts refer to this name.\n\nFor native V2 volumes, the runtime OpenSVC volume name becomes \`{name}-vol-<canonical-name>\`.\n\nExample: canonical name \`newvolume\` -> runtime \`{name}-vol-newvolume\`.`;
const hVolumePool = `**Pool**\n\nOpenSVC storage pool backing this volume. The pool selects where the storage is allocated.\n\nFor legacy-migrated volumes, the canonical name often matches the pool identity because the old runtime grouped storage by pool.`;
const hVolumeRuntime = `**Runtime Name**\n\nActual OpenSVC volume name template generated at provisioning time.\n\n- Native V2 volume: \`{name}-vol-<canonical-name>\`\n- Legacy-migrated volume: preserves old pooled identity such as \`{name}-drbd\`\n\nThis is why canonical name and runtime name can differ.`;
const hVolumeSize = `**Size**\n\nRequested storage size for the backing volume.\n\nWhen the app uses credits, each volume must consume whole credit-sized storage units and each unit has a minimum size. Example layouts include \`10g\`, \`20g\`, or \`10g + 20g\` across multiple volumes.`;
const hVolumeShared = `**Shared**\n\nMarks the volume as shared when the selected OpenSVC pool supports shared storage semantics.\n\nThis option is only available on pools that report shared capability.`;

const hSourceName = `**Name**\n\nStable logical source identity used by mounts.\n\nA source describes content rooted inside a canonical volume, for example a directory root, a git checkout, or an S3-backed source.`;
const hSourceType = `**Type**\n\nSource content type:\n\n- \`directory\`: existing path inside the volume\n- \`git\`: repository checkout stored inside the volume\n- \`s3\`: object storage content mapped into the volume`;
const hSourceVolume = `**Volume**\n\nCanonical volume that owns this source.\n\nThe source base path is interpreted relative to the selected volume's filesystem.`;
const hSourceBasePath = `**Base Path**\n\nRoot path for the source inside its canonical volume.\n\nMounts can map the whole base path or a child \`sub-path\` under it.`;
const hSourceGit = `**Repo / Bucket**\n\nFor \`git\` sources this column shows the repository URL.\nFor \`s3\` sources it shows the bucket name.\n\nUse the source editor to configure the full git or S3 settings.`;

const hMountSource = `**Source**\n\nCanonical source being exposed to the container.\n\nA mount always points at one source and can optionally narrow it to a child \`sub-path\`.`;
const hMountSubPath = `**Sub-path**\n\nOptional path below the source base path.\n\nUse this when you want to mount only a child directory instead of the whole source root.`;
const hMountTargetPath = `**Container Path**\n\nAbsolute destination path inside the container.\n\nThis is where the selected source becomes visible at runtime.`;
const hMountReadOnly = `**Read-only**\n\nWhen enabled, the container receives the mount as read-only.\n\nUse this for configuration or content that should not be modified from inside the app container.`;

function HeaderWithHelp({ label, title, content, onOpenInfo }) {
  return (
    <HStack spacing={1} justify="center">
      <Text as="span">{label}</Text>
      <RMIconButton
        icon={HiQuestionMarkCircle}
        aria-label={`Help for ${title}`}
        onClick={(e) => {
          e?.stopPropagation?.();
          onOpenInfo(title, content);
        }}
        iconFontsize="1rem"
        variant="ghost"
        style={{ opacity: 0.5, minWidth: "1.5rem", height: "1.5rem" }}
      />
    </HStack>
  );
}
HeaderWithHelp.propTypes = {
  label: PropTypes.string.isRequired,
  title: PropTypes.string.isRequired,
  content: PropTypes.string.isRequired,
  onOpenInfo: PropTypes.func.isRequired,
};

// ---- Shared form helpers ----------------------------------------------------

function LabeledInput({ label, helper, ...props }) {
  return (
    <FormControl>
      <FormLabel fontSize="sm" mb={0}>{label}</FormLabel>
      <Input size="sm" {...props} />
      {helper && <FormHelperText fontSize="xs">{helper}</FormHelperText>}
    </FormControl>
  );
}
LabeledInput.propTypes = {
  label: PropTypes.string.isRequired,
  helper: PropTypes.string,
};

function LabeledSelect({ label, helper, children, ...props }) {
  return (
    <FormControl>
      <FormLabel fontSize="sm" mb={0}>{label}</FormLabel>
      <Select size="sm" {...props}>{children}</Select>
      {helper && <FormHelperText fontSize="xs">{helper}</FormHelperText>}
    </FormControl>
  );
}
LabeledSelect.propTypes = {
  label: PropTypes.string.isRequired,
  helper: PropTypes.string,
  children: PropTypes.node,
};

// ---- Volumes ----------------------------------------------------------------

function VolumeForm({ data, onChange, pools, isNew, onSave, onCancel, isSaveDisabled, creditUnitGiB, allowAdvancedSizeOption, advancedSize, onToggleAdvancedSize }) {
  const poolObj = useMemo(
    () => (pools || []).find((p) => (p.Name || p.name || p) === data.pool) || null,
    [pools, data.pool]
  );
  const poolSupportsShared = poolObj ? !!(poolObj.Shared || poolObj.shared) : false;
  const sizeInputValue = useMemo(() => {
    const sizeGiB = sizeToGiB(data.size);
    return Number.isFinite(sizeGiB) ? sizeGiB : undefined;
  }, [data.size]);

  const handlePoolChange = useCallback((e) => {
    const newPool = (pools || []).find((p) => (p.Name || p.name || p) === e.target.value);
    onChange("pool", e.target.value);
    if (data.shared && newPool && !(newPool.Shared || newPool.shared)) {
      onChange("shared", false);
    }
  }, [pools, data.shared, onChange]);

  return (
    <Box borderWidth="1px" borderRadius="md" p={4} mt={2} bg="gray.50">
      <Heading size="xs" mb={3}>{isNew ? "Add Volume" : `Edit: ${data.name}`}</Heading>
      <VStack spacing={3} align="stretch">
        <HStack spacing={3} wrap="wrap">
          {isNew && (
            <LabeledInput label="Name" placeholder="e.g. app-data"
              value={data.name} onChange={(e) => onChange("name", e.target.value)} />
          )}
          <LabeledSelect label="Pool" value={data.pool} onChange={handlePoolChange}>
            <option value="">Select pool</option>
            {(pools || []).map((p) => {
              const name = p.Name || p.name || p;
              const shared = !!(p.Shared || p.shared);
              return <option key={name} value={name}>{name}{shared ? " (shared)" : ""}</option>;
            })}
          </LabeledSelect>
          <FormControl>
            <FormLabel fontSize="sm" mb={0}>Size</FormLabel>
            <HStack spacing={2} align="center">
              <NumberInput
                min={1}
                max={100000}
                step={advancedSize ? 1 : creditUnitGiB}
                inputWidth="90px"
                value={sizeInputValue}
                onChange={(_, valueAsNumber) => {
                  if (!Number.isFinite(valueAsNumber)) {
                    onChange("size", "")
                    return
                  }
                  onChange("size", `${valueAsNumber}g`)
                }}
                containerClassName={styles.inlineNumberInput}
              />
              <Text fontSize="sm" minWidth="2ch">G</Text>
            </HStack>
            <FormHelperText fontSize="xs">
              {advancedSize
                ? "Advanced size mode is enabled. Use whole GiB values; budget limits still apply."
                : `Use whole GiB values. Credit-managed volumes validate in ${creditUnitGiB}G increments.`}
            </FormHelperText>
          </FormControl>
          {allowAdvancedSizeOption && (
            <FormControl>
              <FormLabel fontSize="sm" mb={0}>Advanced Size</FormLabel>
              <Checkbox isChecked={advancedSize} onChange={(e) => onToggleAdvancedSize(e.target.checked)}>
                <Text fontSize="sm">Allow custom size increments</Text>
              </Checkbox>
              <FormHelperText fontSize="xs">
                Disable to use credit-step sizing. Enable to enter a custom GiB size while keeping the total budget limit.
              </FormHelperText>
            </FormControl>
          )}
          <FormControl>
            <FormLabel fontSize="sm" mb={0}>Shared</FormLabel>
            <Tooltip
              label={!data.pool
                ? "Select a pool first"
                : !poolSupportsShared
                  ? `Pool "${data.pool}" does not support sharing`
                  : "Enable shared volume (pool supports sharing)"}
              shouldWrapChildren
            >
              <Checkbox
                isChecked={data.shared && poolSupportsShared}
                isDisabled={!poolSupportsShared}
                onChange={(e) => onChange("shared", e.target.checked)}
              >
                <Text fontSize="sm">Shared</Text>
              </Checkbox>
            </Tooltip>
            <FormHelperText fontSize="xs">
              {poolSupportsShared
                ? "This pool supports shared volumes."
                : data.pool
                  ? "This pool does not support shared volumes."
                  : "Pool sharing capability shown after pool selection."}
            </FormHelperText>
          </FormControl>
        </HStack>
        <HStack>
          <RMButton size="sm" onClick={onSave} isDisabled={isSaveDisabled}>
            {isNew ? "Add Volume" : "Save Changes"}
          </RMButton>
          <RMButton size="sm" variant="ghost" onClick={onCancel}>Cancel</RMButton>
        </HStack>
      </VStack>
    </Box>
  );
}
VolumeForm.propTypes = {
  data: PropTypes.shape({
    name: PropTypes.string,
    pool: PropTypes.string,
    size: PropTypes.string,
    shared: PropTypes.bool,
  }).isRequired,
  onChange: PropTypes.func.isRequired,
  pools: PropTypes.array,
  isNew: PropTypes.bool,
  onSave: PropTypes.func.isRequired,
  onCancel: PropTypes.func.isRequired,
  isSaveDisabled: PropTypes.bool,
  creditUnitGiB: PropTypes.number.isRequired,
  allowAdvancedSizeOption: PropTypes.bool.isRequired,
  advancedSize: PropTypes.bool.isRequired,
  onToggleAdvancedSize: PropTypes.func.isRequired,
};

function VolumesSection({ clusterName, appId, rows, opensvcPools, dispatch, canEdit, physicalVolumeStrategy, onOpenInfo, creditUnitGiB, storageBudgetGiB, allocatedStorageGiB, plannedCredits, allowAdvancedSizeOption }) {
  const [form, setForm] = useState({ mode: IDLE, data: getDefaultVol(creditUnitGiB), original: null, advancedSize: false });
  const [confirm, setConfirm] = useState(null);

  const handleChange = useCallback((key, value) =>
    setForm((f) => ({ ...f, data: { ...f.data, [key]: value } })), []);

  const startAdd = () => setForm({ mode: ADD, data: getDefaultVol(creditUnitGiB), original: null, advancedSize: false });
  const startEdit = (row) => {
    const rowSizeGiB = sizeToGiB(row?.size);
    const useAdvancedSize = allowAdvancedSizeOption && !isStandardCreditSized(rowSizeGiB, creditUnitGiB);
    setForm({ mode: EDIT, data: { ...row }, original: row.name, advancedSize: useAdvancedSize });
  };
  const reset = () => setForm({ mode: IDLE, data: getDefaultVol(creditUnitGiB), original: null, advancedSize: false });
  const handleToggleAdvancedSize = useCallback((checked) => {
    setForm((current) => ({ ...current, advancedSize: checked }));
  }, []);

  const handleSave = useCallback(() => {
    if (form.mode === ADD) dispatch(addCanonicalVolume({ clusterName, appId, vol: form.data }));
    else dispatch(updateCanonicalVolume({ clusterName, appId, volName: form.original, vol: form.data }));
    reset();
  }, [clusterName, appId, form, dispatch]);

  const handleDrop = useCallback((volName) => {
    dispatch(dropCanonicalVolume({ clusterName, appId, volName }));
    setConfirm(null);
  }, [clusterName, appId, dispatch]);

  const currentAllocatedWithoutEditedGiB = useMemo(() => rows.reduce((total, row) => {
    if (!row || row.name === form.original) {
      return total;
    }
    const sizeGiB = sizeToGiB(row.size);
    return Number.isFinite(sizeGiB) ? total + sizeGiB : total;
  }, 0), [form.original, rows]);

  const originalVolumeSizeGiB = useMemo(() => {
    const originalRow = rows.find((row) => row?.name === form.original);
    return sizeToGiB(originalRow?.size);
  }, [form.original, rows]);

  const editedVolumeSizeGiB = useMemo(() => sizeToGiB(form.data.size), [form.data.size]);

  const volumeValidationError = useMemo(() => {
    if (!canEdit || !form.data.size || storageBudgetGiB <= 0) {
      return "";
    }
    if (!Number.isFinite(editedVolumeSizeGiB) || editedVolumeSizeGiB <= 0) {
      return "Volume size must be a valid size value";
    }
    if (!Number.isInteger(editedVolumeSizeGiB)) {
      return "Volume size must use whole GiB values";
    }
    const unchangedGrandfatheredSize = form.original && Number.isFinite(originalVolumeSizeGiB) && editedVolumeSizeGiB === originalVolumeSizeGiB;
    if (editedVolumeSizeGiB < creditUnitGiB) {
      if (unchangedGrandfatheredSize) {
        return "";
      }
      return `Volume size must be at least ${creditUnitGiB}G (1 credit)`;
    }
    const units = editedVolumeSizeGiB / creditUnitGiB;
    if (!form.advancedSize && !Number.isInteger(units)) {
      if (unchangedGrandfatheredSize) {
        return "";
      }
      return `Volume size must be a multiple of ${creditUnitGiB}G`;
    }
    if (currentAllocatedWithoutEditedGiB + editedVolumeSizeGiB > storageBudgetGiB) {
      return `Allocated volume size would exceed the ${storageBudgetGiB}G credit storage budget`;
    }
    return "";
  }, [canEdit, creditUnitGiB, currentAllocatedWithoutEditedGiB, editedVolumeSizeGiB, form.advancedSize, form.data.size, form.original, originalVolumeSizeGiB, storageBudgetGiB]);

  const remainingStorageGiB = useMemo(() => normalizeGiB(storageBudgetGiB - allocatedStorageGiB), [allocatedStorageGiB, storageBudgetGiB]);

  const isSaveDisabled = !canEdit || !form.data.pool || !form.data.size || (form.mode === ADD && !form.data.name) || !!volumeValidationError;

  const columns = useMemo(() => {
    const baseColumns = [
      columnHelper.accessor("name", { header: <HeaderWithHelp label="Canonical Name" title="Canonical Name" content={hVolumeName} onOpenInfo={onOpenInfo} /> }),
      columnHelper.accessor("pool", { header: <HeaderWithHelp label="Pool" title="Pool" content={hVolumePool} onOpenInfo={onOpenInfo} /> }),
      columnHelper.display({
        id: "runtimeName",
        header: <HeaderWithHelp label="Runtime Name" title="Runtime Name" content={hVolumeRuntime} onOpenInfo={onOpenInfo} />,
        cell: (info) => {
          const vol = info.row.original;
          const runtimeName = getRuntimeVolumeNameTemplate(vol, physicalVolumeStrategy);
          return (
            <VStack align="start" spacing={1}>
              <Text>{runtimeName}</Text>
              {vol.runtimeName ? <Badge colorScheme="purple">legacy-derived</Badge> : <Badge colorScheme="blue">per-volume</Badge>}
            </VStack>
          );
        },
      }),
      columnHelper.accessor("size", { header: <HeaderWithHelp label="Size" title="Size" content={hVolumeSize} onOpenInfo={onOpenInfo} /> }),
      columnHelper.accessor("shared", { header: <HeaderWithHelp label="Shared" title="Shared" content={hVolumeShared} onOpenInfo={onOpenInfo} />, cell: (info) => (info.getValue() ? "yes" : "no") }),
    ];

    if (!canEdit) {
      return baseColumns;
    }

    return [...baseColumns, columnHelper.display({
      id: "actions", header: "",
      cell: (info) => (
        <HStack>
          <RMIconButton icon={TbEdit} tooltip="Edit" onClick={() => startEdit(info.row.original)} />
          <RMIconButton icon={HiTrash} tooltip="Remove" onClick={() => setConfirm(info.row.original.name)} />
        </HStack>
      ),
    })];
  }, [canEdit, onOpenInfo, physicalVolumeStrategy]);

  return (
    <VStack align="stretch" spacing={3}>
      {storageBudgetGiB > 0 && (
        <Flex wrap="wrap" gap={2} align="center">
          <Badge colorScheme="blue">Credits: {plannedCredits}</Badge>
          <Badge colorScheme="purple">Budget: {storageBudgetGiB}G</Badge>
          <Badge colorScheme="orange">Allocated: {normalizeGiB(allocatedStorageGiB)}G</Badge>
          <Badge colorScheme={remainingStorageGiB > 0 ? "green" : remainingStorageGiB === 0 ? "yellow" : "red"}>
            Remaining: {remainingStorageGiB}G
          </Badge>
          <Text fontSize="sm" color="gray.500">Each volume must be at least {creditUnitGiB}G and sized in {creditUnitGiB}G increments.</Text>
        </Flex>
      )}
      <DataTable data={rows} columns={columns} />
      {form.mode !== IDLE ? (
        <>
          <VolumeForm data={form.data} onChange={handleChange} pools={opensvcPools}
          isNew={form.mode === ADD} onSave={handleSave} onCancel={reset} isSaveDisabled={isSaveDisabled} creditUnitGiB={creditUnitGiB}
          allowAdvancedSizeOption={allowAdvancedSizeOption} advancedSize={form.advancedSize} onToggleAdvancedSize={handleToggleAdvancedSize} />
          {volumeValidationError && (
            <Text fontSize="sm" color="red.500">{volumeValidationError}</Text>
          )}
        </>
      ) : canEdit ? (
        <RMButton size="sm" alignSelf="flex-start" onClick={startAdd}>Add Volume</RMButton>
      ) : null}
      <ConfirmModal isOpen={!!confirm} closeModal={() => setConfirm(null)}
        onConfirmClick={() => handleDrop(confirm)}
        title="Confirm Delete" body={`Remove volume "${confirm}"?`} />
    </VStack>
  );
}
VolumesSection.propTypes = {
  clusterName: PropTypes.string.isRequired,
  appId: PropTypes.string.isRequired,
  rows: PropTypes.array,
  opensvcPools: PropTypes.array,
  dispatch: PropTypes.func.isRequired,
  canEdit: PropTypes.bool.isRequired,
  physicalVolumeStrategy: PropTypes.string,
  onOpenInfo: PropTypes.func.isRequired,
  creditUnitGiB: PropTypes.number.isRequired,
  storageBudgetGiB: PropTypes.number.isRequired,
  allocatedStorageGiB: PropTypes.number.isRequired,
  plannedCredits: PropTypes.number.isRequired,
  allowAdvancedSizeOption: PropTypes.bool.isRequired,
};

// ---- Sources ----------------------------------------------------------------

const SOURCE_TYPES = ["directory", "git", "s3"];
const defaultSrc = {
  name: "", type: "directory", volumeName: "", basePath: "/",
  repo: "", branch: "", user: "", pass: "",
  endpoint: "", bucket: "", providerName: "", region: "", accessKey: "", secretKey: "", mountDir: "",
};
const SECRET_FIELDS = ["pass", "secretKey"];

function GitCheckStatus({ status }) {
  if (!status) return null;
  if (status === "loading") return <Text fontSize="xs" color="gray.500">Checking…</Text>;
  if (status === "ok") return <Text fontSize="xs" color="green.600">Connection OK</Text>;
  return <Text fontSize="xs" color="red.600">{status}</Text>;
}
GitCheckStatus.propTypes = { status: PropTypes.string };

function SourceForm({ data, onChange, volumes, s3Providers, isNew, onCheckGit, onSave, onCancel, isSaveDisabled }) {
  const [gitCheckStatus, setGitCheckStatus] = useState(null);

  const handleCheckGit = useCallback(async () => {
    setGitCheckStatus("loading");
    try {
      await onCheckGit({ repo: data.repo, branch: data.branch, user: data.user, pass: data.pass });
      setGitCheckStatus("ok");
    } catch (err) {
      setGitCheckStatus(err?.message || "Connection failed");
    }
  }, [data.repo, data.branch, data.user, data.pass, onCheckGit]);

  const handleProviderSelect = useCallback((provName) => {
    onChange("providerName", provName);
    if (provName) {
      onChange("endpoint", "");
      onChange("accessKey", "");
      onChange("secretKey", "");
    }
  }, [onChange]);

  return (
    <Box borderWidth="1px" borderRadius="md" p={4} mt={2} bg="gray.50">
      <Heading size="xs" mb={3}>{isNew ? "Add Source" : `Edit: ${data.name}`}</Heading>
      <VStack spacing={3} align="stretch">
        <HStack spacing={3} wrap="wrap">
          {isNew && (
            <LabeledInput label="Name" placeholder="e.g. app-src"
              value={data.name} onChange={(e) => onChange("name", e.target.value)} />
          )}
          <LabeledSelect label="Type" value={data.type} onChange={(e) => onChange("type", e.target.value)}>
            {SOURCE_TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
          </LabeledSelect>
          <LabeledSelect label="Volume" value={data.volumeName} onChange={(e) => onChange("volumeName", e.target.value)}>
            <option value="">Select volume</option>
            {volumes.map((v) => <option key={v.name} value={v.name}>{v.name}</option>)}
          </LabeledSelect>
          <LabeledInput label="Base Path" placeholder="/"
            value={data.basePath} onChange={(e) => onChange("basePath", e.target.value)} />
        </HStack>

        {data.type === "git" && (
          <>
            <Divider />
            <Heading size="xs" color="purple.600">Git Settings</Heading>
            <HStack spacing={3} wrap="wrap">
              <LabeledInput label="Repo URL" placeholder="https://github.com/org/repo"
                value={data.repo} onChange={(e) => onChange("repo", e.target.value)} />
              <LabeledInput label="Branch" placeholder="main"
                value={data.branch} onChange={(e) => onChange("branch", e.target.value)} />
              <LabeledInput label="User"
                value={data.user} onChange={(e) => onChange("user", e.target.value)} />
              <LabeledInput
                label={isNew ? "Password / Token" : "Password / Token (blank = keep existing on save)"}
                type="password" placeholder={isNew ? "" : "Enter new value to change"}
                value={data.pass} onChange={(e) => onChange("pass", e.target.value)} />
            </HStack>
            <HStack align="center">
              <RMButton size="sm" variant="outline" onClick={handleCheckGit} isDisabled={!data.repo}>
                Test Connection
              </RMButton>
              <GitCheckStatus status={gitCheckStatus} />
              {!isNew && !data.pass && (
                <Text fontSize="xs" color="gray.500">
                  Testing without password — enter it above to test authentication
                </Text>
              )}
            </HStack>
          </>
        )}

        {data.type === "s3" && (
          <>
            <Divider />
            <Heading size="xs" color="orange.600">S3 Settings</Heading>
            {s3Providers.length > 0 && (
              <LabeledSelect label="Cluster Provider (optional)"
                helper="Selecting a provider uses its credentials — clears inline endpoint/keys"
                value={data.providerName}
                onChange={(e) => handleProviderSelect(e.target.value)}>
                <option value="">— use inline credentials —</option>
                {s3Providers.map((p) => (
                  <option key={p.name} value={p.name}>{p.name}</option>
                ))}
              </LabeledSelect>
            )}
            <HStack spacing={3} wrap="wrap">
              <LabeledInput label="Bucket" placeholder="my-bucket"
                value={data.bucket} onChange={(e) => onChange("bucket", e.target.value)} />
              <LabeledInput label="Endpoint" placeholder="https://s3.amazonaws.com"
                helper={data.providerName ? "Not used when a provider is selected" : "Required when no provider is set"}
                isDisabled={!!data.providerName}
                value={data.endpoint} onChange={(e) => onChange("endpoint", e.target.value)} />
              {!data.providerName && (
                <>
                  <LabeledInput label="Region" placeholder="us-east-1"
                    value={data.region} onChange={(e) => onChange("region", e.target.value)} />
                  <LabeledInput label="Access Key"
                    value={data.accessKey} onChange={(e) => onChange("accessKey", e.target.value)} />
                  <LabeledInput
                    label={isNew ? "Secret Key" : "Secret Key (blank = keep existing on save)"}
                    type="password" placeholder={isNew ? "" : "Enter new value to change"}
                    value={data.secretKey} onChange={(e) => onChange("secretKey", e.target.value)} />
                </>
              )}
              <LabeledInput label="Mount Dir"
                helper="FUSE mount point inside container (optional)"
                value={data.mountDir} onChange={(e) => onChange("mountDir", e.target.value)} />
            </HStack>
          </>
        )}

        <HStack>
          <RMButton size="sm" onClick={onSave} isDisabled={isSaveDisabled}>
            {isNew ? "Add Source" : "Save Changes"}
          </RMButton>
          <RMButton size="sm" variant="ghost" onClick={onCancel}>Cancel</RMButton>
        </HStack>
      </VStack>
    </Box>
  );
}
SourceForm.propTypes = {
  data: PropTypes.shape({
    name: PropTypes.string,
    type: PropTypes.string,
    volumeName: PropTypes.string,
    basePath: PropTypes.string,
    repo: PropTypes.string,
    branch: PropTypes.string,
    user: PropTypes.string,
    pass: PropTypes.string,
    bucket: PropTypes.string,
    endpoint: PropTypes.string,
    providerName: PropTypes.string,
    region: PropTypes.string,
    accessKey: PropTypes.string,
    secretKey: PropTypes.string,
    mountDir: PropTypes.string,
  }).isRequired,
  onChange: PropTypes.func.isRequired,
  volumes: PropTypes.array,
  s3Providers: PropTypes.array,
  isNew: PropTypes.bool,
  onCheckGit: PropTypes.func.isRequired,
  onSave: PropTypes.func.isRequired,
  onCancel: PropTypes.func.isRequired,
  isSaveDisabled: PropTypes.bool,
};

function SourcesSection({ clusterName, appId, rows, volumes, dispatch, canEdit, onOpenInfo }) {
  const s3Providers = useSelector(selectClusterS3Providers);
  const [form, setForm] = useState({ mode: IDLE, data: { ...defaultSrc }, original: null });
  const [confirm, setConfirm] = useState(null);

  const handleChange = useCallback((key, value) =>
    setForm((f) => ({ ...f, data: { ...f.data, [key]: value } })), []);

  const startAdd = () => setForm({ mode: ADD, data: { ...defaultSrc }, original: null });
  const startEdit = useCallback((row) => {
    const data = { ...defaultSrc, ...row };
    SECRET_FIELDS.forEach((k) => { data[k] = ""; });
    setForm({ mode: EDIT, data, original: row.name });
  }, []);
  const reset = () => setForm({ mode: IDLE, data: { ...defaultSrc }, original: null });

  const handleSave = useCallback(() => {
    if (form.mode === ADD) dispatch(addCanonicalSource({ clusterName, appId, src: form.data }));
    else dispatch(updateCanonicalSource({ clusterName, appId, srcName: form.original, src: form.data }));
    reset();
  }, [clusterName, appId, form, dispatch]);

  const handleDrop = useCallback((srcName) => {
    dispatch(dropCanonicalSource({ clusterName, appId, srcName }));
    setConfirm(null);
  }, [clusterName, appId, dispatch]);

  // Always tests current form values. If pass is blank: tests without auth (public repos work;
  // private repos will return an auth error, prompting the user to enter their password).
  const handleCheckGit = useCallback(async ({ repo, branch, user, pass }) => {
    const result = await dispatch(checkGitRepo({
      clusterName, appName: appId,
      payload: { repo, branch, user, pass },
    })).unwrap();
    if (result?.data?.ok === false) throw new Error(result?.data?.message || "Connection check failed");
    return result;
  }, [clusterName, appId, dispatch]);

  const isSaveDisabled = useMemo(() => {
    const d = form.data;
    if (!canEdit) return true;
    if (!d.volumeName || !d.basePath) return true;
    if (form.mode === ADD && !d.name) return true;
    if (d.type === "git" && !d.repo) return true;
    if (d.type === "s3" && (!d.bucket || (!d.endpoint && !d.providerName))) return true;
    return false;
  }, [canEdit, form]);

  const columns = useMemo(() => {
    const baseColumns = [
      columnHelper.accessor("name", { header: <HeaderWithHelp label="Name" title="Source Name" content={hSourceName} onOpenInfo={onOpenInfo} /> }),
      columnHelper.accessor("type", {
        header: <HeaderWithHelp label="Type" title="Source Type" content={hSourceType} onOpenInfo={onOpenInfo} />,
        cell: (info) => (
          <Badge colorScheme={info.getValue() === "git" ? "purple" : info.getValue() === "s3" ? "orange" : "blue"}>
            {info.getValue()}
          </Badge>
        ),
      }),
      columnHelper.accessor("volumeName", { header: <HeaderWithHelp label="Volume" title="Source Volume" content={hSourceVolume} onOpenInfo={onOpenInfo} /> }),
      columnHelper.accessor("basePath", { header: <HeaderWithHelp label="Base Path" title="Base Path" content={hSourceBasePath} onOpenInfo={onOpenInfo} /> }),
      columnHelper.display({
        id: "details", header: <HeaderWithHelp label="Repo / Bucket" title="Repo / Bucket" content={hSourceGit} onOpenInfo={onOpenInfo} />,
        cell: (info) => info.row.original.repo || info.row.original.bucket || "",
      }),
    ];

    if (!canEdit) {
      return baseColumns;
    }

    return [...baseColumns, columnHelper.display({
      id: "actions", header: "",
      cell: (info) => (
        <HStack>
          <RMIconButton icon={TbEdit} tooltip="Edit" onClick={() => startEdit(info.row.original)} />
          <RMIconButton icon={HiTrash} tooltip="Remove" onClick={() => setConfirm(info.row.original.name)} />
        </HStack>
      ),
    })];
  }, [canEdit, onOpenInfo, startEdit]);

  return (
    <VStack align="stretch" spacing={3}>
      <DataTable data={rows} columns={columns} />
      {form.mode !== IDLE ? (
        <SourceForm data={form.data} onChange={handleChange} volumes={volumes}
          s3Providers={s3Providers} isNew={form.mode === ADD}
          onCheckGit={handleCheckGit} onSave={handleSave} onCancel={reset} isSaveDisabled={isSaveDisabled} />
      ) : canEdit ? (
        <RMButton size="sm" alignSelf="flex-start" onClick={startAdd}>Add Source</RMButton>
      ) : null}
      <ConfirmModal isOpen={!!confirm} closeModal={() => setConfirm(null)}
        onConfirmClick={() => handleDrop(confirm)}
        title="Confirm Delete" body={`Remove source "${confirm}"?`} />
    </VStack>
  );
}
SourcesSection.propTypes = {
  clusterName: PropTypes.string.isRequired,
  appId: PropTypes.string.isRequired,
  rows: PropTypes.array,
  volumes: PropTypes.array,
  dispatch: PropTypes.func.isRequired,
  canEdit: PropTypes.bool.isRequired,
  onOpenInfo: PropTypes.func.isRequired,
};

// ---- Mounts -----------------------------------------------------------------

const defaultMount = { name: "", sourceName: "", sourceSubPath: "", targetPath: "", readOnly: false };

function MountForm({ data, onChange, sources, dockerImage, clusterName, isNew, onSave, onCancel, isSaveDisabled, dispatch }) {
  const [treeOpen, setTreeOpen] = useState(false);
  const dockerTree = useSelector((state) => state.paths?.current?.dockerTree || {});

  const handleBrowse = useCallback(async () => {
    if (!dockerImage) return;
    await dispatch(getDockerTree({ clusterName, dockerImage }));
    setTreeOpen(true);
  }, [clusterName, dockerImage, dispatch]);

  const handleSelectPath = useCallback((path) => {
    onChange("targetPath", path);
    setTreeOpen(false);
  }, [onChange]);

  return (
    <Box borderWidth="1px" borderRadius="md" p={4} mt={2} bg="gray.50">
      <Heading size="xs" mb={3}>{isNew ? "Add Mount" : `Edit: ${data.targetPath}`}</Heading>
      <VStack spacing={3} align="stretch">
        <HStack spacing={3} wrap="wrap">
          <LabeledSelect label="Source" value={data.sourceName} onChange={(e) => onChange("sourceName", e.target.value)}>
            <option value="">Select source</option>
            {sources.map((s) => <option key={s.name} value={s.name}>{s.name}</option>)}
          </LabeledSelect>
          <LabeledInput label="Sub-path (optional)" placeholder="/"
            value={data.sourceSubPath} onChange={(e) => onChange("sourceSubPath", e.target.value)} />
          <FormControl>
            <FormLabel fontSize="sm" mb={0}>Container path</FormLabel>
            <InputGroup size="sm">
              <Input placeholder="/app/data" value={data.targetPath}
                onChange={(e) => onChange("targetPath", e.target.value)} />
              {dockerImage && (
                <InputRightElement>
                  <RMIconButton icon={HiFolder} tooltip="Browse container paths" size="xs" variant="ghost"
                    onClick={handleBrowse} />
                </InputRightElement>
              )}
            </InputGroup>
          </FormControl>
          <FormControl>
            <FormLabel fontSize="sm" mb={0}>Read-only</FormLabel>
            <Checkbox isChecked={data.readOnly} onChange={(e) => onChange("readOnly", e.target.checked)}>
              <Text fontSize="sm">Read-only</Text>
            </Checkbox>
          </FormControl>
        </HStack>
        <HStack>
          <RMButton size="sm" onClick={onSave} isDisabled={isSaveDisabled}>
            {isNew ? "Add Mount" : "Save Changes"}
          </RMButton>
          <RMButton size="sm" variant="ghost" onClick={onCancel}>Cancel</RMButton>
        </HStack>
      </VStack>
      {treeOpen && (
        <TreeView title="Browse Container Path" treeName="dockerpath"
          nodeToValue={nodeToValue} nodeToString={nodeToString}
          defaultValues={[data.targetPath || "/"]} treeData={dockerTree}
          isOpen={treeOpen} asModal={true}
          onClose={() => setTreeOpen(false)} onSave={handleSelectPath} />
      )}
    </Box>
  );
}
MountForm.propTypes = {
  data: PropTypes.shape({
    sourceName: PropTypes.string,
    sourceSubPath: PropTypes.string,
    targetPath: PropTypes.string,
    readOnly: PropTypes.bool,
  }).isRequired,
  onChange: PropTypes.func.isRequired,
  sources: PropTypes.array,
  dockerImage: PropTypes.string,
  clusterName: PropTypes.string.isRequired,
  isNew: PropTypes.bool,
  onSave: PropTypes.func.isRequired,
  onCancel: PropTypes.func.isRequired,
  isSaveDisabled: PropTypes.bool,
  dispatch: PropTypes.func.isRequired,
};

function MountsSection({ clusterName, appId, rows, sources, dockerImage, dispatch, canEdit, onOpenInfo }) {
  const [form, setForm] = useState({ mode: IDLE, data: { ...defaultMount }, original: null });
  const [confirm, setConfirm] = useState(null);

  const handleChange = useCallback((key, value) =>
    setForm((f) => ({ ...f, data: { ...f.data, [key]: value } })), []);

  const startAdd = () => setForm({ mode: ADD, data: { ...defaultMount }, original: null });
  const startEdit = (row) => setForm({ mode: EDIT, data: { ...defaultMount, ...row }, original: row.targetPath });
  const reset = () => setForm({ mode: IDLE, data: { ...defaultMount }, original: null });

  const handleSave = useCallback(() => {
    if (form.mode === ADD) dispatch(addCanonicalMount({ clusterName, appId, mount: form.data }));
    else dispatch(updateCanonicalMount({ clusterName, appId, targetPath: form.original, mount: form.data }));
    reset();
  }, [clusterName, appId, form, dispatch]);

  const handleDrop = useCallback((targetPath) => {
    dispatch(dropCanonicalMount({ clusterName, appId, targetPath }));
    setConfirm(null);
  }, [clusterName, appId, dispatch]);

  const isSaveDisabled = !canEdit || !form.data.sourceName || !form.data.targetPath;

  const columns = useMemo(() => {
    const baseColumns = [
      columnHelper.accessor("sourceName", { header: <HeaderWithHelp label="Source" title="Mount Source" content={hMountSource} onOpenInfo={onOpenInfo} /> }),
      columnHelper.accessor("sourceSubPath", { header: <HeaderWithHelp label="Sub-path" title="Mount Sub-path" content={hMountSubPath} onOpenInfo={onOpenInfo} /> }),
      columnHelper.accessor("targetPath", { header: <HeaderWithHelp label="Container Path" title="Container Path" content={hMountTargetPath} onOpenInfo={onOpenInfo} /> }),
      columnHelper.accessor("readOnly", { header: <HeaderWithHelp label="RO" title="Read-only" content={hMountReadOnly} onOpenInfo={onOpenInfo} />, cell: (info) => (info.getValue() ? "yes" : "no") }),
    ];

    if (!canEdit) {
      return baseColumns;
    }

    return [...baseColumns, columnHelper.display({
      id: "actions", header: "",
      cell: (info) => (
        <HStack>
          <RMIconButton icon={TbEdit} tooltip="Edit" onClick={() => startEdit(info.row.original)} />
          <RMIconButton icon={HiTrash} tooltip="Remove" onClick={() => setConfirm(info.row.original.targetPath)} />
        </HStack>
      ),
    })];
  }, [canEdit, onOpenInfo]);

  return (
    <VStack align="stretch" spacing={3}>
      <DataTable data={rows} columns={columns} />
      {form.mode !== IDLE ? (
        <MountForm data={form.data} onChange={handleChange} sources={sources}
          dockerImage={dockerImage} clusterName={clusterName}
          isNew={form.mode === ADD} onSave={handleSave} onCancel={reset}
          isSaveDisabled={isSaveDisabled} dispatch={dispatch} />
      ) : canEdit ? (
        <RMButton size="sm" alignSelf="flex-start" onClick={startAdd}>Add Mount</RMButton>
      ) : null}
      <ConfirmModal isOpen={!!confirm} closeModal={() => setConfirm(null)}
        onConfirmClick={() => handleDrop(confirm)}
        title="Confirm Delete" body={`Remove mount at "${confirm}"?`} />
    </VStack>
  );
}
MountsSection.propTypes = {
  clusterName: PropTypes.string.isRequired,
  appId: PropTypes.string.isRequired,
  rows: PropTypes.array,
  sources: PropTypes.array,
  dockerImage: PropTypes.string,
  dispatch: PropTypes.func.isRequired,
  canEdit: PropTypes.bool.isRequired,
  onOpenInfo: PropTypes.func.isRequired,
};

// ---- Top-level canonical storage panel -------------------------------------

export default function CanonicalStorage({ clusterName, appId, deployment, opensvcPools, appConfig, clusterConfig, user }) {
  const dispatch = useDispatch();
  const [action, setAction] = useState({ title: "", body: <></> });
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false);
  const canEdit = !!user?.grants?.["app-deployment"];
  const physicalVolumeStrategy = deployment?.physicalVolumeStrategy || PER_VOLUME_STRATEGY;

  const appVolumes = useMemo(() => deployment?.appVolumes || [], [deployment]);
  const appSources = useMemo(() => deployment?.appSources || [], [deployment]);
  const appMounts = useMemo(() => deployment?.appMounts || [], [deployment]);
  const dockerImage = appConfig?.provAppDockerImg || "";
  const plannedCredits = Number(appConfig?.provAppCreditPlanned || 0);
  const allowAdvancedSizeOption = !!clusterConfig?.provAppVolumeAllowAdvancedSize;
  const creditUnitGiB = useMemo(() => {
    const candidate = sizeToGiB(clusterConfig?.provAppDiskSize || `${DEFAULT_CREDIT_VOLUME_SIZE_GIB}`);
    if (!Number.isFinite(candidate) || candidate <= 0) {
      return DEFAULT_CREDIT_VOLUME_SIZE_GIB;
    }
    return Math.round(candidate);
  }, [clusterConfig?.provAppDiskSize]);
  const storageBudgetGiB = plannedCredits > 0 ? plannedCredits * creditUnitGiB : 0;
  const allocatedStorageGiB = useMemo(() => normalizeGiB(sumVolumeSizesGiB(appVolumes)), [appVolumes]);

  const openInfoModal = useCallback((title, content) => {
    setAction({
      title,
      body: (
        <Box className={modalStyles.infoTooltip}>
          <Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown>
        </Box>
      ),
    });
    setIsCommonModalOpen(true);
  }, []);

  const volumesBody = useMemo(() => (
    <VolumesSection clusterName={clusterName} appId={appId}
      rows={appVolumes} opensvcPools={opensvcPools} dispatch={dispatch}
      canEdit={canEdit} physicalVolumeStrategy={physicalVolumeStrategy} onOpenInfo={openInfoModal}
      creditUnitGiB={creditUnitGiB} storageBudgetGiB={storageBudgetGiB}
      allocatedStorageGiB={allocatedStorageGiB} plannedCredits={plannedCredits} allowAdvancedSizeOption={allowAdvancedSizeOption} />
  ), [allocatedStorageGiB, allowAdvancedSizeOption, appId, appVolumes, canEdit, clusterName, creditUnitGiB, dispatch, openInfoModal, opensvcPools, physicalVolumeStrategy, plannedCredits, storageBudgetGiB]);

  const sourcesBody = useMemo(() => (
    <SourcesSection clusterName={clusterName} appId={appId}
      rows={appSources} volumes={appVolumes} dispatch={dispatch} canEdit={canEdit} onOpenInfo={openInfoModal} />
  ), [appId, appSources, appVolumes, canEdit, clusterName, dispatch, openInfoModal]);

  const mountsBody = useMemo(() => (
    <MountsSection clusterName={clusterName} appId={appId}
      rows={appMounts} sources={appSources} dockerImage={dockerImage} dispatch={dispatch} canEdit={canEdit} onOpenInfo={openInfoModal} />
  ), [appId, appMounts, appSources, canEdit, clusterName, dispatch, dockerImage, openInfoModal]);

  return (
    <Flex direction="column" className={styles.sectionWrapper}>
      <Alert status="info" mb={4} borderRadius="md">
        <AlertIcon />
        <Box>
          <AlertTitle>Canonical Storage Mode (v2)</AlertTitle>
          <AlertDescription fontSize="sm">
            This app uses the canonical storage model. Manage volumes, sources, and mounts below.
            The legacy storages view is a read-only compatibility shadow.
            Volume names are canonical identities; runtime names show the actual OpenSVC volume name template.
            Migrated legacy volumes preserve their original <code>{"{name}-{pool}"}</code> runtime identity.
          </AlertDescription>
        </Box>
      </Alert>
      <VStack spacing={3} align="stretch">
        <AccordionComponent heading="Volumes" body={volumesBody} />
        <AccordionComponent heading="Sources" body={sourcesBody} />
        <AccordionComponent heading="Mounts" body={mountsBody} />
      </VStack>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size="xl" />
    </Flex>
  );
}
CanonicalStorage.propTypes = {
  clusterName: PropTypes.string.isRequired,
  appId: PropTypes.string.isRequired,
  deployment: PropTypes.object,
  opensvcPools: PropTypes.array,
  appConfig: PropTypes.shape({
    provAppDockerImg: PropTypes.string,
    provAppCreditPlanned: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  }),
  clusterConfig: PropTypes.shape({
    provAppDiskSize: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
    provAppVolumeAllowAdvancedSize: PropTypes.bool,
  }),
  user: PropTypes.shape({
    grants: PropTypes.object,
  }),
};
