import { VStack, HStack, Text, Heading, Input, Select, Flex, InputGroup } from '@chakra-ui/react'
import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { HiFolder, HiTrash } from 'react-icons/hi'
import Dropdown from '../../../../../components/Dropdown';
import TextForm from '../../../../../components/TextForm';
import RMIconButton from '../../../../../components/RMIconButton';
import RMButton from '../../../../../components/RMButton';
import styles from './styles.module.scss';
import TreeView from '../../../../../components/Modals/TreeView/TreeView';
import { useDispatch, useSelector } from 'react-redux';
import { showErrorToast } from '../../../../../redux/toastSlice';
import { getDockerTree, getGitTree } from '../../../../../redux/pathSlice';
import { uniqueId } from 'lodash';

const defaultConfirmText = "Are you sure to change this field to: ";

const volumeDirs = [
  { value: 'log', name: 'log' },
  { value: 'var', name: 'var' },
]

const defaultValues = { volumedir: "var", from: "var", to: "", type: "", agents: [] }

const nodeToValue = (node) => node.path;
const nodeToString = (node) => node.name || node.path;


export default React.memo(function Paths({
  clusterName,
  appId,
  dockerImage,
  rows = [],
  gitCloneRows = [],
  fieldName = 'path',
  onRowArrayChange,
  onRowDropIndex,
  onSaveAdd,
}) {

  const dispatch = useDispatch();

  const { gitTree, dockerTree } = useSelector(state => ({
    gitTree: state.paths.current.gitTree || {},
    dockerTree: state.paths.current.dockerTree || {},
  }));

  const [sources, setSources] = useState(volumeDirs);
  const [formData, setFormData] = useState([]);
  const [browseState, setBrowseState] = useState({
    isOpen: false,
    selectedPath: '',
    selectedIndex: null,
    selectedKey: '',
  });

  const { selectedKey, isOpen } = browseState

  const treeData = useMemo(() => {
    if (selectedKey === 'from') {
      return gitTree;
    }
    return dockerTree;
  }, [gitTree, dockerTree, selectedKey]);

  useEffect(() => {
    dispatch(getDockerTree({ clusterName, dockerImage }));
  }, [dockerImage, clusterName]);

  useEffect(() => {
    if (gitCloneRows.length > 0) {
      const newSources = gitCloneRows
        .map(gc => ({ value: gc.volumedir + "/" + gc.dest, name: gc.dest }))
        .filter(item => !sources.some(src => src.value === item.value));

      setSources(prevSources => [...prevSources, ...newSources]);
    }
  }, [gitCloneRows]);

  const handleBrowseGit = (index, repoURL) => {
    let volumedir = formData[index]["volumedir"];
    if (!volumedir) {
      return dispatch(showErrorToast("Cannot browse path without git volumedir set."));
    }

    // split by / and take the first part as the base path
    if (!volumedir.includes("/")) {
      return dispatch(showErrorToast("Cannot browse path without git volumedir set to a valid path."));
    } else {
      let parts = volumedir.split("/");
      let found = false;

      for (let i = 0; i < parts.length; i++) {
        if (parts[i] === "var" || parts[i] === "etc") {
          volumedir = parts[i];
          found = true;
          break;
        }
      }

      if (!found) {
        return dispatch(showErrorToast("Cannot browse path without git volumedir set to data (var) or config (etc)."));
      }

      dispatch(getGitTree({ clusterName, appId, volumedir, repoURL })).then(() => {
        // Open the TreeView modal to select a path
        setBrowseState({
          isOpen: true,
          selectedPath: formData[index]["from"],
          selectedIndex: index,
          selectedKey: "from",
        });
      }).catch((error) => {
        dispatch(showErrorToast(`Failed to fetch git directory tree: ${error.message}`));
      });
    };
  }

  const handleBrowseDocker = (index) => {
    dispatch(getDockerTree({ clusterName, dockerImage })).then(() => {
      // Open the TreeView modal to select a path
      setBrowseState({
        isOpen: true,
        selectedPath: formData[index]["to"],
        selectedIndex: index,
        selectedKey: "to",
      });
    }).catch((error) => {
      dispatch(showErrorToast(`Failed to fetch docker directory tree: ${error.message}`));
    });
  }

  const handleCloseBrowse = () => {
    // Close the TreeView modal
    setBrowseState({
      isOpen: false,
      selectedPath: '',
      selectedIndex: null,
      selectedKey: '',
    });
  };

  const handleSelectPath = (selectedPath) => {
    // Update the formData with the selected path
    const updatedFormData = [...formData];
    if (browseState.selectedIndex !== null) {
      updatedFormData[browseState.selectedIndex] = {
        ...updatedFormData[browseState.selectedIndex],
        [browseState.selectedKey]: selectedPath,
      };
    }
    setFormData(updatedFormData);
    // Close the modal after selection
    handleCloseBrowse();
  };

  const isDefaultPath = useCallback((path) => (["var", "log"].some(dir => dir === path)), []);

  const handleArrayChange = (index, key, value) => {
    setFormData(prev => {
      const updated = [...prev];
      updated[index] = {
        ...updated[index],
        [key]: value,
        ...(key === 'volumedir' && isDefaultPath(value) ? { from: value } : {})
      };
      return updated;
    });

  };


  const handleAddItem = () => {
    setFormData(prevState => [...prevState, { ...defaultValues, id: uniqueId() }]);
  };

  const handleRemoveItem = (index) => {
    setFormData(prevState => [...prevState.filter((_, i) => i !== index)]);
  };

  const validateFormData = (data) => {
    for (const item of data) {
      if (item.from.includes('..') || item.to.includes('..')) {
        return "Relative paths ('..') are not allowed.";
      }
      if (!item.volumedir || !item.from || !item.to) {
        return "All fields (volumedir, from, to) must be filled.";
      }
    }
    return null;
  };


  const handleSaveAdd = () => {
    if (formData.length > 0) {
      // Prevent saving if field is using relative paths (..)
      const errors = validateFormData(formData);
      if (errors) {
        dispatch(showErrorToast({ title: "Invalid Path", description: errors }));
        return;
      }
      onSaveAdd(fieldName, formData).then(() => {
        setFormData([]); // Clear the form after saving
      })
    }
  }

  const selectedValues = useMemo(() => {
    return browseState.selectedPath ? [browseState.selectedPath] : [];
  },[browseState.selectedPath]);

  return (
    <Flex direction="column" className={`${styles.sectionWrapper}`}>
      <VStack spacing={3} align="stretch">
        <Heading as="h3" size="md">
          Saved path mappings
        </Heading>
        {rows?.length > 0 ?
          rows?.map((p, index) => (
            <HStack key={`row_${p.to}`}>
              <Dropdown confirmTitle={"Are you sure to change volumedir: "} selectedValue={p.volumedir} onChange={(value) => onRowArrayChange(fieldName, index, "volumedir", value)} options={volumeDirs} />
              <TextForm confirmTitle={defaultConfirmText} name={`row_${index}.from`} placeholder="From" value={p.from} onSave={(value) => onRowArrayChange(fieldName, index, "from", value)} />
              <TextForm confirmTitle={defaultConfirmText} name={`row_${index}.to`} placeholder="To" value={p.to} onSave={(value) => onRowArrayChange(fieldName, index, "to", value)} />
              <RMIconButton icon={HiTrash} aria-label="Delete Path" onClick={() => onRowDropIndex(fieldName, index)} />
            </HStack>
          )) : (
            <Text>No saved path mappings</Text>
          )}
      </VStack>
      {formData.length > 0 && (
        <VStack spacing={3} align="stretch">
          <Heading as="h3" size="md">
            New Path Mapping
          </Heading>
          <Text>Enter the path mappings for your deployment. Select a volume directory and specify the source and destination paths.</Text>
          {formData.map((p, index) => {
            // Check if p.volumedir is from git clones
            const gc = gitCloneRows.find(gc => gc.volumedir + "/" + gc.dest === p.volumedir);
            return (
              <HStack key={`new_${p.id}`}>
                <Select value={p.volumedir} onChange={(e) => handleArrayChange(index, "volumedir", e.target.value)} >
                  {sources.map(source => (
                    <option key={source.value} value={source.value}>
                      {source.name}
                    </option>
                  ))}
                </Select>
                {!!gc && (
                  <InputGroup>
                    <Input name={`new_${index}.from`} placeholder="From" value={p.from} onChange={(e) => handleArrayChange(index, "from", e.target.value)} />
                    <RMIconButton icon={HiFolder} aria-label="Browse Path" onClick={() => handleBrowseGit(index, gc.repo)} />
                  </InputGroup>
                )}
                <InputGroup>
                  <Input name={`new_${index}.to`} placeholder="To" value={p.to} onChange={(e) => handleArrayChange(index, "to", e.target.value)} />
                  <RMIconButton icon={HiFolder} aria-label="Browse Path" onClick={() => handleBrowseDocker(index)} />
                </InputGroup>
                <RMIconButton icon={HiTrash} aria-label="Delete Path" onClick={() => handleRemoveItem(index)} />
              </HStack>
            )
          })}
        </VStack>
      )}
      {isOpen && (
        <TreeView
          title="Browse Path"
          nodeToValue={nodeToValue}
          nodeToString={nodeToString}
          defaultValues={selectedValues}
          treeData={treeData}
          isOpen={isOpen}
          asModal={true}
          onClose={handleCloseBrowse}
          onSave={handleSelectPath}
        />
      )}
      <VStack spacing={3} align="stretch">
        <HStack>
          {formData?.length > 0 && (
            <RMButton onClick={handleSaveAdd}>
              Save Path
            </RMButton>
          )}
          <RMButton onClick={handleAddItem}>
            Add Path
          </RMButton>
        </HStack>
      </VStack>
    </Flex>
  )
})
