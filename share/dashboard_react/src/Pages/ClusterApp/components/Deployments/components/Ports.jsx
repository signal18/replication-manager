import { VStack, HStack, Text, Heading, Input, Flex } from '@chakra-ui/react'
import { HiTrash } from 'react-icons/hi';
import React, { useState } from 'react';
import TextForm from '../../../../../components/TextForm';
import RMIconButton from '../../../../../components/RMIconButton';
import RMButton from '../../../../../components/RMButton';
import styles from './styles.module.scss';

const defaultConfirmText = "Are you sure to change this field to: ";

export default React.memo(function Ports({
  rows = [],
  fieldName = fieldName,
  onRowArrayChange,
  onRowDropIndex,
  onSaveAdd,
}) {

  const [formData, setFormData] = useState([]);

  const handleArrayChange = (index, value) => {
    setFormData(prevState => [...prevState.map((item, i) => i === index ? value : item)]);
  };

  const handleAddItem = () => {
    setFormData(prevState => [...prevState, ""]);
  };

  const handleRemoveItem = (index) => {
    setFormData(prevState => [...prevState.filter((_, i) => i !== index)]);
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
          Saved Ports
        </Heading>
        { rows?.length > 0 ?
        rows?.map((p, index) => (
          <HStack key={index}>
            <TextForm confirmTitle={defaultConfirmText} pattern="^[0-9]{1,5}(:[0-9]{1,5})?$" placeholder="Container Port" value={p} onSave={(value) => onRowArrayChange(fieldName, index, null, value)} />
            <RMIconButton icon={HiTrash} aria-label="Delete Port" onClick={() => onRowDropIndex(fieldName, index)} />
          </HStack>
        )) : (
          <Text>No saved ports found.</Text>
        )}
      </VStack>
      {formData.length > 0 && (
        <VStack spacing={3} align="stretch">
          <Heading as="h3" size="md">
            Add New Port
          </Heading>
          <Text>Enter container ports in the format: 80 or 80:8080</Text>
          {formData.map((p, index) => (
            <HStack key={index}>
              <Input pattern="^[0-9]{1,5}(:[0-9]{1,5})?$" placeholder="Container Port" value={p} onChange={(e) => handleArrayChange(index, e.target.value)} />
              <RMIconButton icon={HiTrash} aria-label="Delete Port" onClick={() => handleRemoveItem(index)} />
            </HStack>
          ))}
        </VStack>
      )}
      <VStack spacing={3} align="stretch">
        <HStack>
          {formData?.length > 0 && (
            <RMButton onClick={handleSaveAdd}>
              Save Port
            </RMButton>
          )}
          <RMButton onClick={handleAddItem}>
            Add Port
          </RMButton>
        </HStack>
      </VStack>
    </Flex>
  )
})
