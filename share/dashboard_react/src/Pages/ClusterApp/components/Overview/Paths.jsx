import { VStack, HStack, Text, Heading, Input, Select, Flex, InputGroup, Box } from '@chakra-ui/react'
import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { HiFolder, HiTrash } from 'react-icons/hi'
import { TbEdit, TbLinkPlus } from 'react-icons/tb'
import Dropdown from '../../../../components/Dropdown';
import TextForm from '../../../../components/TextForm';
import RMIconButton from '../../../../components/RMIconButton';
import RMButton from '../../../../components/RMButton';
import styles from './styles.module.scss';
import TreeView from '../../../../components/Modals/TreeView/TreeView';
import { useDispatch, useSelector } from 'react-redux';
import { showErrorToast } from '../../../../redux/toastSlice';
import { getDockerTree, getGitTree, hashMurmur } from '../../../../redux/pathSlice';
import { createColumnHelper } from '@tanstack/react-table';
import { DataTable } from '../../../../components/DataTable';

const sourceTypes = [
  { value: '', name: 'Select Source', isChildOption: true },
  { value: 'volume', name: 'Volume', isChildOption: true },
  { value: 'git', name: 'Git', isChildOption: false },
  { value: 's3', name: 'S3', isChildOption: true },
]

const defaultPath = {
  dockerpath: "",
  dockersubpath: "/",
  parentname: "",
  parentpath: "",
  srctype: "",
  srcname: "",
  srcpath: "/",
  srcbasepath: "",
  subpath: "/",
}

const nodeToValue = (node) => { console.log(`nodeToValue: ${JSON.stringify(node)}`); return node.path; };
const nodeToString = (node) => node.name || node.path;

const columnHelper = createColumnHelper()

const PathSection = ({
  rows = [],
  storages = {},
  fieldName = "paths",
  clusterName,
  appId,
  dockerImage,
  onRowArrayChange,
  onRowDropIndex,
  onSaveAdd,
  onPauseAutoReload = () => { },
  onResumeAutoReload = () => { },
}) => {
  const [newForm, setNewForm] = useState({ isVisible: false, parentRow: null, rowdata: null });
  const dispatch = useDispatch();

  const { isVisible, parentRow, rowdata } = newForm;
  const gitRows = storages?.gitClones || [];
  const volumeRows = storages?.volumes || [];
  const s3Rows = storages?.s3Mounts || [];

  const volumeOptions = useMemo(() => {
    return [{ value: "", name: "Select Volume" }, ...volumeRows.map(v => ({ ...v, value: v.name, name: v.name, volumedir: v.volumedir }))];
  }, [volumeRows]);

  const gitOptions = useMemo(() => {
    return [{ value: "", name: "Select Git" }, ...gitRows.map(gc => ({ ...gc, value: gc.name, name: gc.name, volumedir: gc.volumedir }))];
  }, [gitRows]);

  const s3Options = useMemo(() => {
    return [{ value: "", name: "Select S3" }, ...s3Rows.map(s3 => ({ ...s3, value: s3.name, name: s3.name }))];
  }, [s3Rows]);

  const resolvedRows = useMemo(() => rows.map((row) => {
    const { srctype, srcname, srcpath, parentname } = row;
    const parent = rows.find(r => r.name === row.name) || { dockerpath: "", srcpath: "", srctype: "", srcname: "" };
    const vol = srctype === "volume" ? volumeOptions.find(opt => opt.name === srcname) : null;
    const gc = srctype === "git" ? gitOptions.find(opt => opt.value === srcname) : null;
    const s3 = srctype === "s3" ? s3Options.find(opt => opt.value === srcname) : null;
    const srcbasepath = srctype === "volume" ? vol?.volumedir || "" : srctype === "git" ? gc?.volumedir || "" : srctype === "s3" ? s3?.volumedir || "" : "";
    return {
      ...row,
      srcbasepath: srcbasepath,
      subpath: srcbasepath && srcpath && srcpath.startsWith(srcbasepath) ? srcpath.replace(srcbasepath, "").replace("//", "/") : srcpath || "",
      parentname: parentname || parent.value,
      parentpath: parent.dockerpath || parent.srcpath,
      parentRow: parent,
    };
  }), [rows, volumeOptions, gitOptions, s3Options]);

  useEffect(() => {
    if (!dockerImage) {
      return;
    }
    dispatch(getDockerTree({ clusterName, dockerImage }));
  }, [dockerImage, clusterName]);


  const handleAddItem = (parent) => {
    setNewForm({ isVisible: true, parentRow: parent });
    onPauseAutoReload(); // Pause auto-reload when adding a new item
  };

  const handleEditItem = useCallback((rows, row) => {
    setNewForm({ isVisible: true, parentRow: rows?.find(r => r.name === row.parentname), rowdata: row });
  }, []);

  const handleCancel = useCallback(() => {
    setNewForm({ isVisible: false, parentRow: null });
    onResumeAutoReload();
  }, [onResumeAutoReload]);

  const handleSaveAdd = useCallback((formData) => {
    return onSaveAdd(fieldName, formData).then(() => {
      setNewForm({ isVisible: false, parentRow: null });
      onResumeAutoReload();
      return Promise.resolve();
    }, (error) => {
      return Promise.reject(error);
    });
  }, [onSaveAdd, fieldName, onResumeAutoReload]);

  const columnsRowForm = useMemo(
    () => [
      columnHelper.accessor((row) => row.dockerpath, {
        header: 'Path'
      }),
      columnHelper.accessor((row) => row.srctype, {
        header: 'Source Type'
      }),
      columnHelper.accessor((row) => row.srcname, {
        header: 'Source Name'
      }),
      columnHelper.accessor((row) => row.srcbasepath, {
        header: 'Storage Base Path'
      }),
      columnHelper.accessor((row) => row.subpath, {
        header: 'Storage Path'
      }),
      columnHelper.accessor((row) => row.volumename, {
        header: 'Volume'
      }),
      columnHelper.display({
        id: 'actions',
        cell: ({ row }) => (
          <Flex gap={2} align="center">
            {row.original.srctype == "git" && (
              <RMIconButton
                icon={TbLinkPlus}
                tooltip="Add Storage Mapping for Git"
                onClick={() => handleAddItem(row.original)}
              />
            )}
            <RMIconButton
              icon={TbEdit}
              tooltip="Edit Path"
              onClick={() => handleEditItem(resolvedRows, row.original)}
            />
            <RMIconButton
              icon={HiTrash}
              tooltip="Delete Path"
              onClick={() => onRowDropIndex(fieldName, row.index)}
            />
          </Flex>
        ),
      }),
    ],
    [fieldName, onRowArrayChange, onRowDropIndex, rows, gitRows, volumeOptions, gitOptions, s3Options]
  )

  return (
    <Flex direction="column" className={`${styles.sectionWrapper}`}>
      <VStack spacing={3} align="stretch">
        <Heading as="h3" size="md">
          Saved Paths
        </Heading>
        <Box className={styles.tableContainer}>
          <DataTable key="app-variables" data={resolvedRows} columns={columnsRowForm} className={styles.table} enableExpanding={true} enableExpandingNoSubRows={true} />
        </Box>
      </VStack>
      {isVisible ? (
        <VStack spacing={3} align="stretch">
          <Heading as="h3" size="md">
            {rowdata ? `Edit Path: ${rowdata.dockerpath}` : "Add New Path"}
          </Heading>
          <Box className={styles.tableContainer}>
            {rowdata ? (
              <PathRowForm fieldName={fieldName} path={rowdata} clusterName={clusterName} appId={appId} gitRows={gitRows} gitOptions={gitOptions} volumeOptions={volumeOptions} s3Options={s3Options} parentRow={parentRow} dockerImage={dockerImage} onChange={onRowArrayChange} onCancel={handleCancel} />
            ) : (
              <PathNewForm clusterName={clusterName} appId={appId} gitOptions={gitOptions} volumeOptions={volumeOptions} s3Options={s3Options} parentRow={parentRow} onSave={handleSaveAdd} onCancel={handleCancel} />
            )}
          </Box>
        </VStack>
      ) : (
        <VStack spacing={3} align="stretch">
          <HStack>
            <RMButton onClick={handleAddItem}>
              Add Path
            </RMButton>
          </HStack>
        </VStack>
      )}
    </Flex>
  )
}

export default React.memo(PathSection);

const EMPTY_OBJECT = {};

const PathRowForm = React.memo(({ fieldName, path, index, clusterName, appId, gitOptions, volumeOptions, s3Options, parentRow, onChange, onCancel = () => { } }) => {
  const dispatch = useDispatch();
  const p = path || defaultPath;
  const { dockerpath, parentname, srctype, srcname, srcpath } = p;

  const vol = useMemo(() => srctype === "volume" ? volumeOptions.find(opt => opt.name === srcname) : null, [srctype, srcname, volumeOptions]);
  const gc = useMemo(() => srctype === "git" ? gitOptions.find(opt => opt.value === srcname) : null, [srctype, srcname, gitOptions]);
  const s3 = useMemo(() => srctype === "s3" ? s3Options.find(opt => opt.value === srcname) : null, [srctype, srcname, s3Options]);
  const parent = useMemo(() => parentRow || { dockerpath: "", srcpath: "", srctype: "", srcname: "" }, [parentRow]);
  const { dockerpath: parentpath, srctype: parentsrctype, srcpath: parentsrcpath } = parent;

  const dockerTree = useSelector(state => state.paths.current.dockerTree || EMPTY_OBJECT);
  const parentGitTree = useSelector(state => (parentsrctype === "git" ? state.paths.gitTreeList[parentname] : EMPTY_OBJECT));
  const gitTree = useSelector(state => (srctype === "git" ? state.paths.gitTreeList[srcname] : EMPTY_OBJECT));
  const s3Tree = useSelector(state => (srctype === "s3" ? state.paths.s3TreeList[srcname] : EMPTY_OBJECT));

  const { dstTree, dstprefix, dstbase } = useMemo(() => {
    if (parentsrctype === "git") {
      return {
        dstTree: parentGitTree,
        dstprefix: parentpath,
        dstbase: parentsrcpath,
      };
    } else {
      return {
        dstTree: dockerTree,
        dstprefix: "",
        dstbase: "",
      };
    }
  }, [parentsrctype, parentGitTree, dockerTree, parentpath, parentsrcpath]);

  const { srcTree, srcbasepath } = useMemo(() => {
    if (srctype === "vol" && vol) {
      return { srcTree: dockerTree, srcbasepath: vol?.volumedir || "" };
    } else if (srctype === "git" && gc) {
      return { srcTree: gitTree, srcbasepath: gc?.volumedir || "" };
    } else if (srctype === "s3" && s3) {
      return { srcTree: s3Tree, srcbasepath: s3?.volumedir || "" };
    } else {
      return { srcTree: dockerTree, srcbasepath: "" };
    }
  }, [srctype, gitTree, s3Tree, dockerTree]);

  const subpath = useMemo(() => {
    if (srcbasepath != "" && srcpath.startsWith(srcbasepath)) {
      return srcpath.replace(srcbasepath, "").replace("//", "/");
    } else {
      return srcpath;
    }
  }, [srcpath, srcbasepath]);

  const onRowArrayChange = useCallback((fieldName, index, key, value) => {
    if (value.includes("..")) {
      dispatch(showErrorToast(`Invalid path: ${value}`));
      return;
    }
    onChange(fieldName, index, key, value);
  }, [])

  const handleChangeSourceType = useCallback((newvalue) => {
    if (srctype != newvalue) {
      onRowArrayChange(fieldName, index, "srctype", newvalue);
    }
  }, [srctype, fieldName, index, onRowArrayChange]);

  const handleSubPathChange = useCallback((value) => {
    if (value.includes("..")) {
      dispatch(showErrorToast(`Invalid subpath: ${value}`));
      return;
    }
    const newSrcPath = !srcbasepath || value.startsWith(srcbasepath) ? value : (srcbasepath || "") + (value.startsWith("/") ? value : `/${value}`);
    onRowArrayChange(fieldName, index, "srcpath", newSrcPath);
  }, [fieldName, index, onRowArrayChange, srcbasepath]);

  useEffect(() => {
    if (gc) {
      dispatch(getGitTree({ clusterName: clusterName, appId: appId, gitName: gc.name }))
        .catch((error) => {
          dispatch(showErrorToast(`Failed to fetch git directory tree: ${error.message}`));
        });
    }
  }, [gc, clusterName, appId, dispatch]);

  const srcDropdown = useMemo(() => {
    let srcOptions = [];
    let srcLabel = '';
    switch (srctype) {
      case 'volume':
        srcOptions = volumeOptions;
        srcLabel = 'Volume :';
        break;
      case 'git':
        srcOptions = gitOptions;
        srcLabel = 'Git :';
        break;
      case 's3':
        srcOptions = s3Options;
        srcLabel = 'S3 :';
        break;
      default:
        srcOptions = [];
        srcLabel = '';
    }
    return (
      <Flex direction="column" flex="1">
        <Text mb={1}>{srcLabel}</Text>
        <Dropdown key={srctype} confirmTitle={"Source changed"} placeholder="Source" options={srcOptions} selectedValue={srcname} onChange={(value) => onRowArrayChange(fieldName, index, "srcname", value)} />
        {srcbasepath && (<Text key={srcname} mb={1} fontSize="sm" color="gray.500">Basepath: {srcbasepath}</Text>)}
      </Flex>
    );
  }, [srctype, volumeOptions, gitOptions, s3Options]);

  const handleOnTreeSelect = useCallback((subpaths) => {
    if (typeof subpaths === 'string') {
      subpaths = subpaths.split(',').map(item => item.trim());
    }
    return subpaths.map((subpath) => {
      return subpath.startsWith("/") ? subpath : "/" + subpath;
    });
  }, [fieldName, index, onRowArrayChange]);

  return (
    <Flex className={styles.variableRowForm} w="100%" align="flex-start" gap={4}>
      <Flex direction="column" flex="1" minW="300px" gap={2}>
        {parentname && (
          <Flex direction="column" flex="1">
            <Text mb={1}>Base Path: {parentpath}</Text>
          </Flex>
        )}
        <Flex direction="column" flex="1">
          <Text mb={1}>{parentname ? "Sub Path" : "Docker Path:"}</Text>
          <TextForm confirmTitle={"Dockerpath changed"} name={`row_${index}.dockerpath`} placeholder="To" value={dockerpath} onSave={(value) => onRowArrayChange(fieldName, index, "dockerpath", value)} isTree={true} treeNodeToValue={nodeToValue} treeNodeToString={nodeToString} treeData={dstTree} />
        </Flex>
        <Flex direction="column" flex="1">
          <Text mb={1}>Source Type:</Text>
          <Dropdown placeholder="Source Type" options={sourceTypes} selectedValue={srctype} onChange={(option) => handleChangeSourceType(option.value)} />
        </Flex>
        {srctype && (<>
          {srcDropdown}
          {srcname && (
            <Flex direction="column" flex="1">
              <Text mb={1}>Storage Path:</Text>
              {srctype === "git" ? (
                <TextForm confirmTitle={"Source path changed"} name={`row_${index}.subpath`} placeholder="To" value={subpath} onSave={(value) => handleSubPathChange(value)} isTree={true} treeNodeToValue={nodeToValue} treeNodeToString={nodeToString} treeData={srcTree} onTreeSelect={handleOnTreeSelect} />
              ) : (
                <TextForm confirmTitle={"Source path changed"} name={`row_${index}.subpath`} placeholder="To" value={subpath} onSave={(value) => handleSubPathChange(value)} />
              )}
            </Flex>
          )}
        </>)}
        <Flex direction="column" flex="1">
          <HStack spacing={2} mt={4}>
            <RMButton onClick={onCancel}>
              Close Form
            </RMButton>
          </HStack>
        </Flex>
      </Flex>
    </Flex>
  )
})

const PathNewForm = React.memo(({ clusterName, appId, parentRow, gitOptions, volumeOptions, s3Options, onSave = () => { }, onCancel = () => { } }) => {
  const dispatch = useDispatch();

  const [path, setPath] = useState(defaultPath);
  const [browseState, setBrowseState] = useState({
    isOpen: false,
    selectedPath: '',
    selectedKey: '',
  });

  useEffect(() => {
    if (parentRow) {
      setPath(prev => ({
        ...prev,
        parentname: parentRow.name,
        parentpath: parentRow.dockerpath || parentRow.srcpath,
      }));
    } else {
      setPath(defaultPath);
    }
  }, [parentRow]);

  const { dockerpath, dockersubpath, srctype, srcname, srcbasepath, srcpath, subpath } = path;
  const { isOpen, selectedPath, selectedKey } = browseState;
  const defaultExpandedValues = useMemo(() => [selectedPath], [selectedPath]);

  const vol = useMemo(
    () => srctype === 'volume' ? volumeOptions.find(opt => opt.name === srcname) : null,
    [srctype, srcname, volumeOptions]
  );
  const gc = useMemo(
    () => srctype === 'git' ? gitOptions.find(opt => opt.value === srcname) : null,
    [srctype, srcname, gitOptions]
  );
  const s3 = useMemo(
    () => srctype === 's3' ? s3Options.find(opt => opt.value === srcname) : null,
    [srctype, srcname, s3Options]
  );

  const srcDropdown = useMemo(() => {
    let srcOptions = [];
    let srcLabel = '';
    switch (srctype) {
      case 'volume':
        srcOptions = volumeOptions;
        srcLabel = 'Volume :';
        break;
      case 'git':
        srcOptions = gitOptions;
        srcLabel = 'Git :';
        break;
      case 's3':
        srcOptions = s3Options;
        srcLabel = 'S3 :';
        break;
      default:
        srcOptions = [];
        srcLabel = '';
    }
    return (
      <Flex direction="column" flex="1">
        <Text mb={1}>{srcLabel}</Text>
        <Dropdown key={srctype} placeholder="Source" options={srcOptions} selectedValue={srcname} onChange={(option) => handleArrayChange("srcname", option.value)} />
        {srcbasepath && (<Text key={srcname} mb={1} fontSize="sm" color="gray.500">Basepath: {srcbasepath}</Text>)}
      </Flex>
    );
  }, [srctype, volumeOptions, gitOptions, s3Options]);

  const parent = useMemo(() => (parentRow || { name: "", dockerpath: "", srcpath: "", srctype: "", srcname: "" }), [parentRow]);
  const { name: parentname, dockerpath: parentpath, srctype: parentsrctype, srcname: parentsrcname, srcpath: parentsrcpath } = parent;

  const newSourceTypes = useMemo(() => {
    if (parentname) return sourceTypes.filter(type => type.isChildOption);
    return sourceTypes;
  }, [sourceTypes, parentname]);

  // Redux selectors - memoized by React-Redux
  const dockerTree = useSelector(state => state.paths.current.dockerTree || EMPTY_OBJECT);
  const parentGitTree = useSelector(state => parentsrctype === "git" ? state.paths.gitTreeList[parentsrcname] : EMPTY_OBJECT);
  const gitTree = useSelector(state => srctype === "git" ? state.paths.gitTreeList[srcname] : EMPTY_OBJECT);
  const s3Tree = useSelector(state => srctype === "s3" ? state.paths.s3TreeList[srcname] : EMPTY_OBJECT);

  const { dstTree, dstprefix, dstbase } = useMemo(() => {
    if (parentsrctype === "git") {
      return {
        dstTree: parentGitTree,
        dstprefix: parentpath,
        dstbase: parentsrcpath,
      };
    }
    return {
      dstTree: dockerTree,
      dstprefix: "",
      dstbase: "",
    };
  }, [parentsrctype, parentpath, parentsrcpath, parentGitTree, dockerTree]);

  const srcTree = useMemo(() => {
    if (srctype === "git") return gitTree;
    if (srctype === "s3") return s3Tree;
    return null;
  }, [srctype, gitTree, s3Tree]);

  const { treeData, treePrefix, treeBase } = useMemo(() => {
    return selectedKey === "subpath"
      ? { treeData: srcTree, treePrefix: "", treeBase: "" }
      : { treeData: dstTree, treePrefix: dstprefix, treeBase: dstbase };
  }, [selectedKey, srcTree, dstTree, dstprefix, dstbase]);

  const handleArrayChange = useCallback((key, value) => {
    setPath(prev => {
      const newPath = { ...prev, [key]: value };
      if (key === "parentname") {
        newPath.dockersubpath = "/";
      } else if (key === "dockersubpath") {
        newPath.dockersubpath = (parentpath + (value.startsWith("/") ? value : `/${value}`)).replace("//", "/");
      } else if (key === "srcname") {
        if (prev.srctype === "volume") {
          const found = volumeOptions.find(opt => opt.name === value);
          newPath.srcbasepath = found ? found.volumedir : "";
        } else if (prev.srctype === "git") {
          const found = gitOptions.find(opt => opt.value === value);
          newPath.srcbasepath = found ? found.volumedir : "";
        } else if (prev.srctype === "s3") {
          const found = s3Options.find(opt => opt.value === value);
          newPath.srcbasepath = found ? found.volumedir : "";
        } else {
          newPath.srcbasepath = "";
        }
        newPath.srcpath = newPath.srcbasepath + (newPath.subpath.startsWith("/") ? newPath.subpath : `/${newPath.subpath}`);
      } else if (key === "srctype") {
        newPath.srcname = "";
        newPath.srcbasepath = "";
        newPath.subpath = "/";
      } else if (key === "subpath") {
        newPath.subpath = value;
        newPath.srcpath = prev.srcbasepath + (value.startsWith("/") ? value : `/${value}`);
      }

      return newPath;
    });
  }, [parentpath]);

  const handleDockerSubPathChange = useCallback((value) => {
    if (value.includes("..")) {
      dispatch(showErrorToast({ message: `Invalid subpath: ${value}` }));
      return;
    }
    setPath(prev => {
      const newPath = { ...prev, dockersubpath: value };
      newPath.dockerpath = (parentpath + (value.startsWith("/") ? value : `/${value}`)).replace("//", "/");
      return newPath;
    });
  }, [dispatch, parentpath]);

  const handleBrowseSource = useCallback(() => {
    if (srctype !== "git") return;
    dispatch(getGitTree({ clusterName, appId, gitName: srcname })).then(() => {
      setBrowseState({
        isOpen: true,
        selectedPath: subpath,
        selectedKey: "subpath",
      });
    });
  }, [dispatch, clusterName, appId, srctype, srcname, subpath]);

  const handleBrowseDestination = useCallback(() => {
    console.log(`browsedst start: ${Date.now()}`);
    if (parentsrctype === "git") {
      dispatch(getGitTree({ clusterName, appId, gitName: parentsrcname })).then(() => {
        setBrowseState({
          isOpen: true,
          selectedPath: dockersubpath || "/",
          selectedKey: "dockersubpath",
        });
      });
    } else {
      let newselectedKey = "dockerpath";
      let newselectedPath = dockerpath || "/";
      if (parentpath) {
        newselectedKey = "dockersubpath";
        newselectedPath = dockersubpath || "/";
      }
      setBrowseState({
        isOpen: true,
        selectedPath: newselectedPath,
        selectedKey: newselectedKey,
      });
    }
  }, [dispatch, clusterName, appId, parentsrctype, parentsrcname, dockerpath, parentpath]);

  const handleCloseBrowse = useCallback(() => {
    setBrowseState({ isOpen: false, selectedPath: '', selectedKey: '' });
  }, []);

  const handleChangeSourceType = useCallback((newvalue) => {
    if (srctype != newvalue) {
      handleArrayChange("srctype", newvalue);
    }
  }, [srctype])

  const handleSelectPath = useCallback((newpath) => {
    if (selectedKey) {
      if (selectedKey === "dockersubpath") {
        handleDockerSubPathChange(newpath);
      } else {
        handleArrayChange(selectedKey, newpath);
      }
    }
    handleCloseBrowse();
  }, [handleArrayChange, handleCloseBrowse, selectedKey]);


  const valid = useMemo(() => {
    if (!dockerpath || dockerpath.includes("..") || dockerpath == "/") return false;
    if (subpath && subpath.includes("..")) return false;

    if (srctype === "volume") return !!vol?.name;
    if (srctype === "git") return !!gc?.value;
    if (srctype === "s3") return !!s3?.value;

    return true;
  }, [dockerpath, subpath, srctype, vol, gc, s3]);

  const handleSaveAdd = useCallback(() => {
    if (valid) {
      onSave([path]);
    } else {
      dispatch(showErrorToast({ message: "Invalid path configuration. Please check the inputs. make sure dockerpath is not root or containing parent relative paths (..)" }));
    }
  }, [onSave, valid, path]);

  const handleCancel = useCallback(() => {
    setPath(defaultPath);
    onCancel();
  }, [onCancel]);

  return (
    <Flex className={styles.VolumeRowForm} w="100%" align="flex-start" gap={4}>
      <Flex direction="column" flex="1" minW="300px" gap={2}>
        {parentname && (
          <Flex direction="column" flex="1">
            <Text mb={1}>Base Path: {parentpath}</Text>
          </Flex>
        )}
        <Flex direction="column" flex="1">
          <Text mb={1}>{parentname ? "Sub Path" : "Docker Path:"}</Text>
          <InputGroup>
            {parentsrctype === "git" ? (
              <Input key={"dockersubpath"} name={`newpath.dockersubpath`} placeholder="Path" value={dockersubpath} onChange={(e) => handleDockerSubPathChange(e.target.value)} />
            ) : (
              <Input key={"dockerpath"} name={`newpath.dockerpath`} placeholder="Path" value={dockerpath} onChange={(e) => handleArrayChange("dockerpath", e.target.value)} />
            )}
            <RMIconButton icon={HiFolder} aria-label="Browse Path" onClick={handleBrowseDestination} />
          </InputGroup>
          {parentname && (<Text key={parentname} mb={1} fontSize="sm" color="gray.500">Fullpath: {dockerpath}</Text>)}
        </Flex>
        <Flex direction="column" flex="1">
          <Text mb={1}>Source Type:</Text>
          <Dropdown placeholder="Source Type" options={newSourceTypes} selectedValue={srctype} onChange={(option) => handleChangeSourceType(option.value)} />
        </Flex>
        {srctype && (<>
          {srcDropdown}
          {srcname && (
            <Flex direction="column" flex="1">
              <Text mb={1}>Storage Path:</Text>
              <InputGroup>
                <Input key={`${srctype}-${srcname}`} name={`newpath.subpath`} placeholder="Subpath" value={subpath} onChange={(e) => handleArrayChange("subpath", e.target.value)} />
                {srctype === "git" && (<RMIconButton icon={HiFolder} aria-label="Browse Path" onClick={handleBrowseSource} />)}
              </InputGroup>
            </Flex>
          )}
        </>)}
        <Flex direction="column" flex="1">
          <HStack spacing={2} mt={4}>
            <RMButton onClick={handleCancel}>
              Clear Form
            </RMButton>
            <RMButton onClick={handleSaveAdd} isDisabled={!valid}>
              Save Path
            </RMButton>
          </HStack>
        </Flex>
      </Flex>
      {isOpen && (
        <TreeView
          title="Browse Path"
          key={selectedKey}
          treeName={selectedKey}
          nodeToValue={nodeToValue}
          nodeToString={nodeToString}
          defaultValues={defaultExpandedValues}
          treeData={treeData}
          isOpen={isOpen}
          asModal={true}
          onClose={handleCloseBrowse}
          onSave={handleSelectPath}
        />
      )}
    </Flex>
  )
})