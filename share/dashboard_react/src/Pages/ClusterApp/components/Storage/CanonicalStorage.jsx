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
import { HiTrash, HiFolder } from "react-icons/hi";
import { TbEdit } from "react-icons/tb";
import AccordionComponent from "../../../../components/AccordionComponent";
import { DataTable } from "../../../../components/DataTable";
import RMButton from "../../../../components/RMButton";
import RMIconButton from "../../../../components/RMIconButton";
import ConfirmModal from "../../../../components/Modals/ConfirmModal";
import TreeView from "../../../../components/Modals/TreeView/TreeView";
import {
  addCanonicalVolume, updateCanonicalVolume, dropCanonicalVolume,
  addCanonicalSource, updateCanonicalSource, dropCanonicalSource,
  addCanonicalMount, updateCanonicalMount, dropCanonicalMount,
  selectClusterS3Providers,
} from "../../../../redux/clusterSlice";
import { checkGitRepo, getDockerTree } from "../../../../redux/pathSlice";
import styles from "./styles.module.scss";

const columnHelper = createColumnHelper();
const IDLE = "idle";
const EDIT = "edit";
const ADD = "add";

const nodeToValue = (node) => (node.type === "directory" && !node.path.endsWith("/") ? node.path + "/" : node.path);
const nodeToString = (node) => node.name || node.path;

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

const defaultVol = { name: "", pool: "", size: "1g", shared: false };

function VolumeForm({ data, onChange, pools, isNew, onSave, onCancel, isSaveDisabled }) {
  const poolObj = useMemo(
    () => (pools || []).find((p) => (p.Name || p.name || p) === data.pool) || null,
    [pools, data.pool]
  );
  const poolSupportsShared = poolObj ? !!(poolObj.Shared || poolObj.shared) : false;

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
          <LabeledInput label="Size" placeholder="e.g. 2g"
            value={data.size} onChange={(e) => onChange("size", e.target.value)} />
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
};

function VolumesSection({ clusterName, appId, rows, opensvcPools, dispatch }) {
  const [form, setForm] = useState({ mode: IDLE, data: { ...defaultVol }, original: null });
  const [confirm, setConfirm] = useState(null);

  const handleChange = useCallback((key, value) =>
    setForm((f) => ({ ...f, data: { ...f.data, [key]: value } })), []);

  const startAdd = () => setForm({ mode: ADD, data: { ...defaultVol }, original: null });
  const startEdit = (row) => setForm({ mode: EDIT, data: { ...row }, original: row.name });
  const reset = () => setForm({ mode: IDLE, data: { ...defaultVol }, original: null });

  const handleSave = useCallback(() => {
    if (form.mode === ADD) dispatch(addCanonicalVolume({ clusterName, appId, vol: form.data }));
    else dispatch(updateCanonicalVolume({ clusterName, appId, volName: form.original, vol: form.data }));
    reset();
  }, [clusterName, appId, form, dispatch]);

  const handleDrop = useCallback((volName) => {
    dispatch(dropCanonicalVolume({ clusterName, appId, volName }));
    setConfirm(null);
  }, [clusterName, appId, dispatch]);

  const isSaveDisabled = !form.data.pool || !form.data.size || (form.mode === ADD && !form.data.name);

  const columns = useMemo(() => [
    columnHelper.accessor("name", { header: "Name" }),
    columnHelper.accessor("pool", { header: "Pool" }),
    columnHelper.accessor("size", { header: "Size" }),
    columnHelper.accessor("shared", { header: "Shared", cell: (info) => (info.getValue() ? "yes" : "no") }),
    columnHelper.display({
      id: "actions", header: "",
      cell: (info) => (
        <HStack>
          <RMIconButton icon={TbEdit} tooltip="Edit" onClick={() => startEdit(info.row.original)} />
          <RMIconButton icon={HiTrash} tooltip="Remove" onClick={() => setConfirm(info.row.original.name)} />
        </HStack>
      ),
    }),
  ], []);

  return (
    <VStack align="stretch" spacing={3}>
      <DataTable data={rows} columns={columns} />
      {form.mode !== IDLE ? (
        <VolumeForm data={form.data} onChange={handleChange} pools={opensvcPools}
          isNew={form.mode === ADD} onSave={handleSave} onCancel={reset} isSaveDisabled={isSaveDisabled} />
      ) : (
        <RMButton size="sm" alignSelf="flex-start" onClick={startAdd}>Add Volume</RMButton>
      )}
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

function SourcesSection({ clusterName, appId, rows, volumes, dispatch }) {
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
    if (!d.volumeName || !d.basePath) return true;
    if (form.mode === ADD && !d.name) return true;
    if (d.type === "git" && !d.repo) return true;
    if (d.type === "s3" && (!d.bucket || (!d.endpoint && !d.providerName))) return true;
    return false;
  }, [form]);

  const columns = useMemo(() => [
    columnHelper.accessor("name", { header: "Name" }),
    columnHelper.accessor("type", {
      header: "Type",
      cell: (info) => (
        <Badge colorScheme={info.getValue() === "git" ? "purple" : info.getValue() === "s3" ? "orange" : "blue"}>
          {info.getValue()}
        </Badge>
      ),
    }),
    columnHelper.accessor("volumeName", { header: "Volume" }),
    columnHelper.accessor("basePath", { header: "Base Path" }),
    columnHelper.display({
      id: "details", header: "Repo / Bucket",
      cell: (info) => info.row.original.repo || info.row.original.bucket || "",
    }),
    columnHelper.display({
      id: "actions", header: "",
      cell: (info) => (
        <HStack>
          <RMIconButton icon={TbEdit} tooltip="Edit" onClick={() => startEdit(info.row.original)} />
          <RMIconButton icon={HiTrash} tooltip="Remove" onClick={() => setConfirm(info.row.original.name)} />
        </HStack>
      ),
    }),
  ], [startEdit]);

  return (
    <VStack align="stretch" spacing={3}>
      <DataTable data={rows} columns={columns} />
      {form.mode !== IDLE ? (
        <SourceForm data={form.data} onChange={handleChange} volumes={volumes}
          s3Providers={s3Providers} isNew={form.mode === ADD}
          onCheckGit={handleCheckGit} onSave={handleSave} onCancel={reset} isSaveDisabled={isSaveDisabled} />
      ) : (
        <RMButton size="sm" alignSelf="flex-start" onClick={startAdd}>Add Source</RMButton>
      )}
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

function MountsSection({ clusterName, appId, rows, sources, dockerImage, dispatch }) {
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

  const isSaveDisabled = !form.data.sourceName || !form.data.targetPath;

  const columns = useMemo(() => [
    columnHelper.accessor("sourceName", { header: "Source" }),
    columnHelper.accessor("sourceSubPath", { header: "Sub-path" }),
    columnHelper.accessor("targetPath", { header: "Container Path" }),
    columnHelper.accessor("readOnly", { header: "RO", cell: (info) => (info.getValue() ? "yes" : "no") }),
    columnHelper.display({
      id: "actions", header: "",
      cell: (info) => (
        <HStack>
          <RMIconButton icon={TbEdit} tooltip="Edit" onClick={() => startEdit(info.row.original)} />
          <RMIconButton icon={HiTrash} tooltip="Remove" onClick={() => setConfirm(info.row.original.targetPath)} />
        </HStack>
      ),
    }),
  ], []);

  return (
    <VStack align="stretch" spacing={3}>
      <DataTable data={rows} columns={columns} />
      {form.mode !== IDLE ? (
        <MountForm data={form.data} onChange={handleChange} sources={sources}
          dockerImage={dockerImage} clusterName={clusterName}
          isNew={form.mode === ADD} onSave={handleSave} onCancel={reset}
          isSaveDisabled={isSaveDisabled} dispatch={dispatch} />
      ) : (
        <RMButton size="sm" alignSelf="flex-start" onClick={startAdd}>Add Mount</RMButton>
      )}
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
};

// ---- Top-level canonical storage panel -------------------------------------

export default function CanonicalStorage({ clusterName, appId, deployment, opensvcPools, appConfig }) {
  const dispatch = useDispatch();

  const appVolumes = useMemo(() => deployment?.appVolumes || [], [deployment]);
  const appSources = useMemo(() => deployment?.appSources || [], [deployment]);
  const appMounts = useMemo(() => deployment?.appMounts || [], [deployment]);
  const dockerImage = appConfig?.provAppDockerImg || "";

  const volumesBody = useMemo(() => (
    <VolumesSection clusterName={clusterName} appId={appId}
      rows={appVolumes} opensvcPools={opensvcPools} dispatch={dispatch} />
  ), [clusterName, appId, appVolumes, opensvcPools, dispatch]);

  const sourcesBody = useMemo(() => (
    <SourcesSection clusterName={clusterName} appId={appId}
      rows={appSources} volumes={appVolumes} dispatch={dispatch} />
  ), [clusterName, appId, appSources, appVolumes, dispatch]);

  const mountsBody = useMemo(() => (
    <MountsSection clusterName={clusterName} appId={appId}
      rows={appMounts} sources={appSources} dockerImage={dockerImage} dispatch={dispatch} />
  ), [clusterName, appId, appMounts, appSources, dockerImage, dispatch]);

  return (
    <Flex direction="column" className={styles.sectionWrapper}>
      <Alert status="info" mb={4} borderRadius="md">
        <AlertIcon />
        <Box>
          <AlertTitle>Canonical Storage Mode (v2)</AlertTitle>
          <AlertDescription fontSize="sm">
            This app uses the canonical storage model. Manage volumes, sources, and mounts below.
            The legacy storages view is a read-only compatibility shadow.
          </AlertDescription>
        </Box>
      </Alert>
      <VStack spacing={3} align="stretch">
        <AccordionComponent heading="Volumes" body={volumesBody} />
        <AccordionComponent heading="Sources" body={sourcesBody} />
        <AccordionComponent heading="Mounts" body={mountsBody} />
      </VStack>
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
  }),
};
