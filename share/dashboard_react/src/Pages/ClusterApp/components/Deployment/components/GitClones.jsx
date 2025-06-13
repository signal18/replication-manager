import { VStack, HStack, Input, Heading, Text, Flex, Select } from '@chakra-ui/react'
import { HiTrash } from 'react-icons/hi'
import React, { useState } from 'react'
import TextForm from '../../../../../components/TextForm'
import RMIconButton from '../../../../../components/RMIconButton';
import RMButton from '../../../../../components/RMButton';
import styles from './styles.module.scss';
import Dropdown from '../../../../../components/Dropdown';
import { pauseAutoReload } from '../../../../../redux/clusterSlice';
import { uniqueId } from 'lodash';

const volumeDirs = [
  { value: 'etc', name: 'config' },
  { value: 'var', name: 'data' },
]

const defaultConfirmText = "Are you sure to change this field to: ";

const initialRow = {
  volumedir: "var",
  dest: "",
  repo: "",
  branch: "",
  user: "",
  pass: ""
}

export default React.memo(function GitClones({
  rows = [],
  fieldName = "gitClones",
  onRowArrayChange,
  onRowDropIndex,
  onSaveAdd,
  onPauseAutoReload = () => {},
  onResumeAutoReload = () => {},
}) {

  const [formData, setFormData] = useState([]);

  const handleArrayChange = (index, key, value) => {
    setFormData(prevState => (prevState.map((item, i) => i === index ? { ...item, [key]: value } : item)));
  };

  const handleAddItem = () => {
    setFormData(prevState => ([...prevState, {...initialRow, id: uniqueId() }]));
    onPauseAutoReload(); // Pause auto-reload when adding a new item
  };

  const handleRemoveItem = (index) => {
    setFormData(prevState => {
          const newState = [...prevState.filter((_, i) => i !== index)];
          if (newState.length === 0) {
            onResumeAutoReload(); // Resume auto-reload when no items left
          }
          return newState;
        });
  };

  const handleSaveAdd = () => {
    if (formData.length > 0) {
      onSaveAdd(fieldName, formData).then(() => {
        setFormData([]); // Clear the form after saving
        onResumeAutoReload(); // Resume auto-reload after saving
      })
    }
  }

  return (
    <Flex direction="column" className={`${styles.sectionWrapper}`}>
      <VStack spacing={3} align="stretch">
        <Heading as="h3" size="md">
          Saved Git Clones
        </Heading>
        {rows?.length > 0 ?
          rows?.map((gc, index) => (
            <HStack key={`row_${gc.dest}`}>
              <Dropdown confirmTitle={"Are you sure to change volumedir: "} selectedValue={gc.volumedir} onChange={(value) => onRowArrayChange(fieldName, index, "volumedir", value)} options={volumeDirs} />
              <TextForm confirmTitle={defaultConfirmText} name={`row_${gc.dest}.dest`} placeholder="Directory Name" value={gc.dest} onSave={(value) => onRowArrayChange(fieldName, index, "dest", value)} />
              <TextForm confirmTitle={defaultConfirmText} name={`row_${gc.dest}.repo`} placeholder="Repo URL" value={gc.repo} onSave={(value) => onRowArrayChange(fieldName, index, "repo", value)} />
              <TextForm confirmTitle={defaultConfirmText} name={`row_${gc.dest}.branch`} placeholder="Branch" value={gc.branch} onSave={(value) => onRowArrayChange(fieldName, index, "branch", value)} />
              <TextForm confirmTitle={defaultConfirmText} name={`row_${gc.dest}.user`} placeholder="Git User" value={gc.user} onSave={(value) => onRowArrayChange(fieldName, index, "user", value)} />
              <TextForm confirmTitle={defaultConfirmText} name={`row_${gc.dest}.pass`} type="password" placeholder="Secret" value={gc.pass} onSave={(value) => onRowArrayChange(fieldName, index, "pass", value)} />
              <RMIconButton
                icon={HiTrash}
                aria-label="Delete Git Clones"
                onClick={() => onRowDropIndex(fieldName, index)}
              />
            </HStack>
          )) : (
            <Text>No saved Git Clones found.</Text>
          )
        }
      </VStack>
      {formData.length > 0 && (
        <VStack spacing={3} align="stretch">
          <Heading as="h3" size="md">
            New Git Clones
          </Heading>
          <Text>Enter Git clone details below. You can add multiple entries.</Text>
          {formData.map((gc, index) => (
            <HStack key={`new_${gc.id}`}>
              <Select value={gc.volumedir} onChange={(e) => handleArrayChange(index, "volumedir", e.target.value)} >
                {volumeDirs.map(opt => (
                  <option key={opt.value} value={opt.value}>
                    {opt.name}
                  </option>
                ))}
              </Select>
              <Input name={`new_${gc.id}.dest`} placeholder="Directory Name" value={gc.dest} onChange={(e) => handleArrayChange(index, "dest", e.target.value)} />
              <Input name={`new_${gc.id}.repo`} placeholder="Repo URL" value={gc.repo} onChange={(e) => handleArrayChange(index, "repo", e.target.value)} />
              <Input name={`new_${gc.id}.branch`} placeholder="Branch" value={gc.branch} onChange={(e) => handleArrayChange(index, "branch", e.target.value)} />
              <Input name={`new_${gc.id}.user`} placeholder="Git User" value={gc.user} onChange={(e) => handleArrayChange(index, "user", e.target.value)} />
              <Input name={`new_${gc.id}.pass`} type="password" placeholder="Secret" value={gc.pass} onChange={(e) => handleArrayChange(index, "pass", e.target.value)} />
              <RMIconButton
                icon={HiTrash}
                aria-label="Delete Git Clones"
                onClick={() => handleRemoveItem(index)}
              />
            </HStack>
          ))}
        </VStack>
      )}
      <VStack spacing={3} align="stretch">
        <HStack>
          {formData?.length > 0 && (
            <RMButton onClick={handleSaveAdd}>
              Save Git Clone
            </RMButton>
          )}
          <RMButton onClick={handleAddItem}>
            Add Git Clone
          </RMButton>
        </HStack>
      </VStack>
    </Flex>
  )
})
