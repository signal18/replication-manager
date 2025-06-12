import { VStack, HStack, Text, Input, Select, Heading, Flex } from '@chakra-ui/react'
import { HiTrash } from 'react-icons/hi'
import TextForm from '../../../../../components/TextForm';
import Dropdown from '../../../../../components/Dropdown';
import RMIconButton from '../../../../../components/RMIconButton';
import RMButton from '../../../../../components/RMButton';
import styles from './styles.module.scss';
import React, { useState } from 'react';

const defaultConfirmText = "Are you sure you want to change this field to: ";

const variableTypes = [
  { value: 'secret', name: 'Secret' },
  { value: 'env', name: 'Env' },
]

export default React.memo(function Variables({
  rows = [],
  fieldName = fieldName,
  onRowArrayChange,
  onRowDropIndex,
  onSaveAdd,
}) {

  const [formData, setFormData] = useState([]);

  const handleArrayChange = (index, key, value) => {
    setFormData(prevState => (prevState.map((item, i) => i === index ? { ...item, [key]: value } : item)));
  };

  const handleAddItem = () => {
    setFormData(prevState => ([...prevState, { name: "", type: "secret", value: "", agents: [] }]));
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
          Saved Variables
        </Heading>
        { rows?.length > 0 ?
        rows?.map((v, index) => (
          <HStack key={index}>
            <TextForm confirmTitle={defaultConfirmText} name={`variables[${index}].name`} placeholder="Name" value={v.name} onSave={(value) => onRowArrayChange(fieldName, index, "name", value)} isDisabled={v.locked} />
            <Dropdown id={`variables[${index}].type`} confirmTitle={"Are you sure to change variable type: "} selectedValue={v.type} onChange={(e) => onRowArrayChange(fieldName, index, "type", e.target.value)} options={variableTypes} isDisabled={v.locked} />
            {v.type === "secret" ? (
              <TextForm confirmTitle={defaultConfirmText} name={`variables[${index}].secret`} type="password" placeholder="Secret" value={v.value} onSave={(value) => onRowArrayChange(fieldName, index, "value", value)} />
            ) : (
              <TextForm confirmTitle={defaultConfirmText} name={`variables[${index}].env`} placeholder="Env" value={v.value} onSave={(value) => onRowArrayChange(fieldName, index, "value", value)} />
            )}
            <RMIconButton icon={HiTrash} aria-label="Delete Variable" onClick={() => onRowDropIndex(fieldName, index)} />
          </HStack>
        )) : (
          <Text>No saved variables found.</Text>
        )
      }
      </VStack>
      {formData.length > 0 && (
        <VStack spacing={3} align="stretch">
          <Heading as="h3" size="md">
            Add New Variables
          </Heading>
          <Text>Enter variables to be used in the deployment. Choose type as Secret or Env.</Text>
          {formData.map((v, index) => (
            <HStack key={index}>
              <Input name={`variables[${index}].name`} placeholder="Name" value={v.name} onChange={(e) => handleArrayChange(index, "name", e.target.value)} />
              <Select value={v.type} onChange={(e) => handleArrayChange(index, "type", e.target.value)}>
                <option value="secret">Secret</option>
                <option value="env">Env</option>
              </Select>
              {v.type === "secret" ? (
                <Input name={`variables[${index}].secret`} type="password" placeholder="Secret" value={v.value} onChange={(e) => handleArrayChange(index, "value", e.target.value)} />
              ) : (
                <Input name={`variables[${index}].env`} placeholder="Env" value={v.value} onChange={(e) => handleArrayChange(index, "value", e.target.value)} />
              )}
              <RMIconButton icon={HiTrash} aria-label="Delete Variable" onClick={() => handleRemoveItem(index)}
              />
            </HStack>
          ))}
        </VStack>
      )}
      <VStack spacing={3} align="stretch">
        <HStack>
          {formData.length > 0 && (
            <RMButton onClick={handleSaveAdd}>
              Save Variables
            </RMButton>
          )}
          <RMButton onClick={handleAddItem}>
            Add Variable
          </RMButton>
        </HStack>
      </VStack>
    </Flex>
  )
})
