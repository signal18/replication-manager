import { VStack, HStack, Input, Heading, Text, Flex, Select } from '@chakra-ui/react'
import { HiTrash } from 'react-icons/hi'
import React, { useState } from 'react'
import TextForm from '../../../../../components/TextForm'
import RMIconButton from '../../../../../components/RMIconButton';
import RMButton from '../../../../../components/RMButton';
import styles from './styles.module.scss';
import Dropdown from '../../../../../components/Dropdown';

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
}) {

  const [formData, setFormData] = useState([]);

  const handleArrayChange = (index, key, value) => {
    setFormData(prevState => (prevState.map((item, i) => i === index ? { ...item, [key]: value } : item)));
  };

  const handleAddItem = () => {
    setFormData(prevState => ([...prevState, initialRow]));
  };

  const handleRemoveItem = (index) => {
    setFormData(prevState => (prevState.filter((_, i) => i !== index)));
  };

  const handleSaveAdd = () => {
    if (formData.length > 0) {
      onSaveAdd(fieldName, formData).then(() => {
        setFormData([]); // Clear the form after saving
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
            <HStack key={`row_${index}`}>
              <Dropdown confirmTitle={"Are you sure to change volumedir: "} selectedValue={gc.volumedir} onChange={(value) => onRowArrayChange(fieldName, index, "volumedir", value)} options={volumeDirs} />
              <TextForm confirmTitle={defaultConfirmText} name={`row_${index}.dest`} placeholder="Directory Name" value={gc.dest} onSave={(value) => onRowArrayChange(fieldName, index, "dest", value)} />
              <TextForm confirmTitle={defaultConfirmText} name={`row_${index}.repo`} placeholder="Repo URL" value={gc.repo} onSave={(value) => onRowArrayChange(fieldName, index, "repo", value)} />
              <TextForm confirmTitle={defaultConfirmText} name={`row_${index}.branch`} placeholder="Branch" value={gc.branch} onSave={(value) => onRowArrayChange(fieldName, index, "branch", value)} />
              <TextForm confirmTitle={defaultConfirmText} name={`row_${index}.user`} placeholder="Git User" value={gc.user} onSave={(value) => onRowArrayChange(fieldName, index, "user", value)} />
              <TextForm confirmTitle={defaultConfirmText} name={`row_${index}.pass`} type="password" placeholder="Secret" value={gc.pass} onSave={(value) => onRowArrayChange(fieldName, index, "pass", value)} />
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
            <HStack key={`new_${index}`}>
              <Select value={gc.volumedir} onChange={(e) => handleArrayChange(index, "volumedir", e.target.value)} >
                {volumeDirs.map(opt => (
                  <option key={opt.value} value={opt.value}>
                    {opt.name}
                  </option>
                ))}
              </Select>
              <Input name={`new_${index}.dest`} placeholder="Directory Name" value={gc.dest} onChange={(e) => handleArrayChange(index, "dest", e.target.value)} />
              <Input name={`new_${index}.repo`} placeholder="Repo URL" value={gc.repo} onChange={(e) => handleArrayChange(index, "repo", e.target.value)} />
              <Input name={`new_${index}.branch`} placeholder="Branch" value={gc.branch} onChange={(e) => handleArrayChange(index, "branch", e.target.value)} />
              <Input name={`new_${index}.user`} placeholder="Git User" value={gc.user} onChange={(e) => handleArrayChange(index, "user", e.target.value)} />
              <Input name={`new_${index}.pass`} type="password" placeholder="Secret" value={gc.pass} onChange={(e) => handleArrayChange(index, "pass", e.target.value)} />
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
