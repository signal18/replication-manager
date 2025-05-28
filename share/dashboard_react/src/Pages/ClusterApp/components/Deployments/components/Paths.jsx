import { VStack, HStack, Text, Heading, Input, Select, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import { HiTrash } from 'react-icons/hi'
import Dropdown from '../../../../../components/Dropdown';
import TextForm from '../../../../../components/TextForm';
import RMIconButton from '../../../../../components/RMIconButton';
import RMButton from '../../../../../components/RMButton';
import styles from './styles.module.scss';

const defaultConfirmText = "Are you sure to change this field to: ";

const volumeDirs = [
  { value: 'etc', name: 'etc' },
  { value: 'log', name: 'log' },
  { value: 'var', name: 'var' },
]

export default React.memo(function Paths({
  rows = [],
  fieldName = 'path',
  onRowArrayChange,
  onRowDropIndex,
  onSaveAdd,
}) {

  const [formData, setFormData] = useState([]);

  const handleArrayChange = (index, key, value) => {
    setFormData(prevState => [...prevState.map((item, i) => i === index ? { ...item, [key]: value } : item)]);
  };

  const handleAddItem = () => {
    setFormData(prevState => [...prevState, { id: Date.now(), volumedir: "var", from: "", to: "", type: "", agents: [] }]);
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
          {formData.map((p, index) => (
            <HStack key={`new_${p.id}`}>
              <Select value={p.volumedir} onChange={(e) => handleArrayChange(index, "volumedir", e.target.value)} >
                {volumeDirs.map(opt => (
                  <option key={opt.value} value={opt.value}>
                    {opt.name}
                  </option>
                ))}
              </Select>
              <Input name={`new_${index}.from`} placeholder="From" value={p.from} onChange={(e) => handleArrayChange(index, "from", e.target.value)} />
              <Input name={`new_${index}.to`} placeholder="To" value={p.to} onChange={(e) => handleArrayChange(index, "to", e.target.value)} />
              <RMIconButton icon={HiTrash} aria-label="Delete Path" onClick={() => handleRemoveItem(index)} />
            </HStack>
          ))}
        </VStack>
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
