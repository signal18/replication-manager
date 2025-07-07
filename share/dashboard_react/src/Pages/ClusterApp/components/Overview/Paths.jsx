import { VStack, HStack, Text, Heading, Input, Select, Flex, InputGroup, Box } from '@chakra-ui/react'
import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { HiFolder, HiTrash } from 'react-icons/hi'
import Dropdown from '../../../../components/Dropdown';
import TextForm from '../../../../components/TextForm';
import RMIconButton from '../../../../components/RMIconButton';
import RMButton from '../../../../components/RMButton';
import styles from './styles.module.scss';
import TreeView from '../../../../components/Modals/TreeView/TreeView';
import { useDispatch, useSelector } from 'react-redux';
import { showErrorToast } from '../../../../redux/toastSlice';
import { getDockerTree, getGitTree, hashMurmur } from '../../../../redux/pathSlice';
import { uniqueId } from 'lodash';
import { createColumnHelper } from '@tanstack/react-table';
import { DataTable } from '../../../../components/DataTable';

const sourceTypes = [
  { value: '', name: 'Select Source' },
  { value: 'git', name: 'git' },
  { value: 's3', name: 's3' },
]

const defaultPath = {
  dockerpath: "",
  sourcetype: "",
  gitclone: "",
  s3: "",
  volumename: "",
  subpath: "",
  volumepath: ""
}

const nodeToValue = (node) => node.path;
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
  const [isVisible, setIsVisible] = useState(false);
  const dispatch = useDispatch();

  const gitRows = storages?.gitClones || [];
  const volumeRows = storages?.volumes || [];
  const s3Rows = storages?.s3Directories || [];
  
  const volumeOptions = useMemo(() => {
    return [{ value: "", name:"Select Volume"}, ...volumeRows.map(v => ({ value: v.name, name: v.name, volumedir: v.volumedir }))];
  }, [volumeRows]);

  const gitOptions = useMemo(() => {
    return [{ value: "", name:"Select Git"}, ...gitRows.map(gc => ({ value: gc.name, name: gc.name, volumedir: gc.volumedir }))];
  }, [gitRows]);

  const s3Options = useMemo(() => {
    return [{ value: "", name:"Select S3"}, ...s3Rows.map(s3 => ({ value: s3.name, name: s3.name }))];
  }, [s3Rows]);

  useEffect(() => {
    if (!dockerImage) {
      return;
    }
    dispatch(getDockerTree({ clusterName, dockerImage }));
  }, [dockerImage, clusterName]);


  const handleAddItem = () => {
    setIsVisible(true);
    onPauseAutoReload(); // Pause auto-reload when adding a new item
  };

  const handleCancel = () => {
    setIsVisible(false); // Hide the form without saving
    onResumeAutoReload(); // Resume auto-reload after canceling
  };

  const handleSaveAdd = (formData) => {
    onSaveAdd(fieldName, formData).then(() => {
      setIsVisible(false); // Hide the form after saving
      onResumeAutoReload(); // Resume auto-reload after saving
      return Promise.resolve();
    }, (error) => {
      return Promise.reject(error);
    });
  }

  const columnsRowForm = useMemo(
    () => [
      columnHelper.accessor((row) => row.dockerpath, {
        header: 'Path'
      }),
      columnHelper.accessor((row) => row.volumename, {
        header: 'Volume'
      }),
      columnHelper.accessor((row) => row.gitclone, {
        header: 'Git Clone'
      }),
      columnHelper.accessor((row) => row.volumepath, {
        header: 'Volume Path'
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
            return (<PathRowForm fieldName={fieldName} path={row.original} index={row.index} clusterName={clusterName} appId={appId} gitRows={gitRows} gitOptions={gitOptions} volumeOptions={volumeOptions} s3Options={s3Options} dockerImage={dockerImage} onChange={onRowArrayChange} />);
          },
        },
        cell: () => null,
      }
    ],
    [fieldName, onRowArrayChange, onRowDropIndex, gitRows, volumeOptions, gitOptions, s3Options]
  )

  return (
    <Flex direction="column" className={`${styles.sectionWrapper}`}>
      <VStack spacing={3} align="stretch">
        <Heading as="h3" size="md">
          Saved Paths
        </Heading>
        <Box className={styles.tableContainer}>
          <DataTable key="app-variables" data={rows} columns={columnsRowForm} className={styles.table} enableExpanding={true} enableExpandingNoSubRows={true} />
        </Box>
      </VStack>
      {isVisible ? (
        <VStack spacing={3} align="stretch">
          <Heading as="h3" size="md">
            Add New Path
          </Heading>
          <Box className={styles.tableContainer}>
            <PathNewForm clusterName={clusterName} appId={appId} gitRows={gitRows} gitOptions={gitOptions} volumeOptions={volumeOptions} s3Options={s3Options} onSave={handleSaveAdd} onCancel={handleCancel} />
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

const PathRowForm = React.memo(({ fieldName, path, index, clusterName, appId, gitOptions, volumeOptions, s3Options, onChange }) => {  
  const dispatch = useDispatch();
  const p = path || defaultPath;
  const vol = volumeOptions.find(vol => vol.name === p.volumename);
  const gc = gitOptions.find(gc => gc.value === p.gitclone);
  const hash = useMemo(() => (gc?.repo ? hashMurmur(gc.repo) : null), [gc?.repo]);
  const gitTree = useSelector(state => (hash ? state.paths.gitTreeList[hash] : EMPTY_OBJECT));
  const dockerTree = useSelector(state => state.paths.current.dockerTree || EMPTY_OBJECT);

  const { dockerpath, volumename, gitclone, volumepath } = p;

  const gitList = useMemo(() => {
    if(vol){
      return gitOptions.filter((gi) => gi.volumedir === vol.volumedir)
    } else {
      return gitOptions
    }
  }, [vol, gitOptions]);

  const subpath = useMemo(() => {
    let path = p.volumepath
    // Remove the leading /gc.volumedir from volumepath to get the subpath
    if (gc && path.startsWith(gc.volumedir)) {
      path = path.substring(gc.volumedir.length);

      if (path.includes("//")) {
        path = path.replace("//", "/");
      }
    }
    return path;
  }, [gc, p.volumepath]);

  const onSubPathChange = useCallback((value) => {
    if (gc) {
      value = `/${gc.volumedir}/${value}`; // Prepend gc.volumedir to the subpath
      // trim double //
      if (value.includes("//")) {
        value = value.replace("//", "/");
      }
    }
    onChange(fieldName, index, "volumepath", value);
  }, [fieldName, index, onChange, gc]);

  const onRowArrayChange = (fieldName, index, key, value) => {
    if (value.includes("..")) {
      dispatch(showErrorToast(`Invalid path: ${value}`));
      return;
    }
    onChange(fieldName, index, key, value);
  };

  useEffect(() => {
    if (gc) {
      dispatch(getGitTree({ clusterName: clusterName, appId: appId, gitName: gc.name }))
        .catch((error) => {
          dispatch(showErrorToast(`Failed to fetch git directory tree: ${error.message}`));
        });
    }
  }, [gc, clusterName, appId, dispatch]);

  return (
    <Flex className={styles.variableRowForm} w="100%" align="flex-start" gap={4}>
      <Flex direction="column" flex="1" minW="300px" gap={2}>
        <Flex direction="column" flex="1">
          <Text mb={1}>Docker Path:</Text>
          <TextForm confirmTitle={"Dockerpath changed"} name={`row_${index}.dockerpath`} placeholder="To" value={dockerpath} onSave={(value) => onRowArrayChange(fieldName, index, "dockerpath", value)} isTree={true} nodeToValue={nodeToValue} nodeToString={nodeToString} treeData={dockerTree} />
        </Flex>
        <Flex direction="column" flex="1">
          <Text mb={1}>Volume:</Text>
          <Dropdown placeholder="Volume" options={volumeOptions} selectedValue={volumename} onChange={(option) => onRowArrayChange(fieldName, index, "volumename", option.value)} />
        </Flex>
        <Flex direction="column" flex="1">
          <Text mb={1}>Git Clone:</Text>
          <Dropdown placeholder="Git Clone" options={gitList} selectedValue={gitclone} onChange={(option) => onRowArrayChange(fieldName, index, "gitclone", option.value)} />
        </Flex>
        <Flex direction="column" flex="1">
          <Text mb={1}>Subpath:</Text>
          { gc ? (
          <TextForm confirmTitle={"Subpath changed"} name={`row_${index}.subpath`} placeholder="From" value={subpath} onSave={(value) => onSubPathChange(value)} isTree={true} nodeToValue={nodeToValue} nodeToString={nodeToString} treeData={gitTree} />
          ) : (
            <TextForm confirmTitle={"Subpath changed"} name={`row_${index}.subpath`} placeholder="From" value={subpath} onSave={(value) => onSubPathChange(value)} />
          )}
        </Flex>
      </Flex>
    </Flex>
  )
})

const PathNewForm = React.memo(({ clusterName, appId, gitRows, gitOptions, volumeOptions, s3Options, onSave = () => { }, onCancel = () => { } }) => {
  const dispatch = useDispatch();
  const gitTree = useSelector(state => state.paths.current.gitTree || EMPTY_OBJECT);
  const dockerTree = useSelector(state => state.paths.current.dockerTree || EMPTY_OBJECT);
  const [path, setPath] = useState(defaultPath);
  const [browseState, setBrowseState] = useState({
    isOpen: false,
    selectedPath: '',
    selectedKey: '',
  });
  const {isOpen, selectedPath, selectedKey} = browseState;
  const { gitclone, subpath, volumename, volumepath, dockerpath } = path;
  const vol = volumeOptions.find(vol => vol.name === volumename);
  const gc = gitOptions.find(gc => gc.name === gitclone);

  const gitList = useMemo(() => {
    if(vol){
      return gitOptions.filter((gi) => gi.volumedir === vol.volumedir)
    } else {
      return gitOptions
    }
  }, [vol, gitOptions]);

  const treeData = useMemo(() => {
    if (selectedKey === 'subpath' && gc) {
      return gitTree;
    }
    return dockerTree;
  }, [gitTree, dockerTree, selectedKey]);


  const handleBrowseGit = (gitname) => {
    
    if (!gitRows.some(gc => gc.name === gitname)) {
      return dispatch(showErrorToast({ title: `Git clone "${gitname}" not found.`}));
    }

    dispatch(getGitTree({ clusterName, appId, gitName: gitname })).then((resp) => {
      // Open the TreeView modal to select a path
      setBrowseState({
        isOpen: true,
        selectedPath: subpath,
        selectedKey: "subpath",
      });

      return resp
    })
  };

  const handleBrowseDocker = () => {
    setBrowseState({
      isOpen: true,
      selectedPath: dockerpath,
      selectedKey: "dockerpath",
    });
  };

  const handleCloseBrowse = () => {
    // Close the TreeView modal
    setBrowseState({
      isOpen: false,
      selectedPath: '',
      selectedKey: '',
    });
  };

  const handleSubPathChange = (value) => {
    let newvalue = value.trim();
    if (gc) {
      newvalue = `/${gc.volumedir}/${newvalue}`; // Prepend gc.volumedir to the subpath
    }
    if (value.includes("//")) {
      value = value.replace("//", "/");
    }
    setPath((prev) => ({ ...prev, subpath: value, volumepath: newvalue }));
  };

  const handleArrayChange = (key, value) => {
    setPath((prev) => ({ ...prev, [key]: value }));
  }

  const handleSelectPath = useCallback((newpath) => {
    // Update the formData with the selected path
    if (selectedKey) {
      if (selectedKey === "subpath") {
        handleSubPathChange(newpath);
      } else {
        handleArrayChange(selectedKey, newpath);
      }
    }
    // Close the modal after selection
    handleCloseBrowse();
  }, [gc, handleCloseBrowse, selectedKey]);

  const valid = useMemo(() => {
    return dockerpath && volumename && !subpath.includes('..') && !dockerpath.includes('..');
  }, [dockerpath, volumename, subpath]);

  const handleSaveAdd = () => {
    if (valid) {
      onSave([path])
    }
  };

  const handleCancel = () => {
    setPath(defaultPath); // Reset form on cancel
    onCancel();
  };

  return (
    <Flex className={styles.VolumeRowForm} w="100%" align="flex-start" gap={4}>
      <Flex direction="column" flex="1" minW="300px" gap={2}>
        <Flex direction="column" flex="1">
          <Text mb={1}>Destination:</Text>
          <InputGroup>
            <Input name={`newpath.dockerpath`} placeholder="Path" value={dockerpath} onChange={(e) => handleArrayChange("dockerpath", e.target.value)} />
            <RMIconButton icon={HiFolder} aria-label="Browse Path" onClick={handleBrowseDocker} />
          </InputGroup>
        </Flex>
        <Flex direction="column" flex="1">
          <Text mb={1}>Volume:</Text>
          <Dropdown placeholder="Volume" options={volumeOptions} selectedValue={volumename} onChange={(option) => handleArrayChange("volumename", option.value)} />
        </Flex>
        <Flex direction="column" flex="1">
          <Text mb={1}>Git Clone:</Text>
          <Dropdown placeholder="Git Clone" options={gitList} selectedValue={gitclone} onChange={(option) => handleArrayChange("gitclone", option.value)} />
        </Flex>
        <Flex direction="column" flex="1">
          <Text mb={1}>Subpath:</Text>
          <InputGroup>
            <Input name={`newpath.subpath`} placeholder="Subpath" value={subpath} onChange={(e) => handleSubPathChange(e.target.value)} />
            { gc && gc.name && (
            <RMIconButton icon={HiFolder} aria-label="Browse Path" onClick={() => handleBrowseGit(gitclone)} />
            )}
          </InputGroup>
        </Flex>
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
          nodeToValue={nodeToValue}
          nodeToString={nodeToString}
          defaultPath={selectedPath}
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