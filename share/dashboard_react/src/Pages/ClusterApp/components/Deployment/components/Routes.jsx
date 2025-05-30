import { VStack, HStack, Text, Heading, Input, Select, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import { HiTrash } from 'react-icons/hi'
import TextForm from '../../../../../components/TextForm';
import RMIconButton from '../../../../../components/RMIconButton';
import RMButton from '../../../../../components/RMButton';
import styles from './styles.module.scss';

const defaultConfirmText = "Are you sure to change this field to: ";

export default React.memo(function Routes({
  rows = [],
  fieldName = 'routes',
  onRowArrayChange,
  onRowDropIndex,
  onSaveAdd,
}) {

  const [formData, setFormData] = useState([]);

  const handleArrayChange = (index, key, value) => {
    setFormData(prevState => [...prevState.map((item, i) => i === index ? { ...item, [key]: value } : item)]);
  };

  const handleAddItem = () => {
    setFormData(prevState => [...prevState, { id: Date.now(), cname: "", port: ""}]);
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

  const sanitizePort = (port) => {
    const sanitizedPort = parseInt(port, 10);
    return isNaN(sanitizedPort) || sanitizedPort < 1 ? "" : sanitizedPort > "65535" ? "65535" : `${sanitizedPort}`;
  }

  return (
    <Flex direction="column" className={`${styles.sectionWrapper}`}>
      <VStack spacing={3} align="stretch">
        <Heading as="h3" size="md">
          Saved route mappings
        </Heading>
        {rows?.length > 0 ?
          rows?.map((p, index) => (
            <HStack key={`row_${p.port}`}>
              <TextForm confirmTitle={defaultConfirmText} name={`row_${index}.cname`} placeholder="CNAME" value={p.cname} onSave={(value) => onRowArrayChange(fieldName, index, "cname", value)} />
              <TextForm confirmTitle={defaultConfirmText} pattern='^[0-9]{1,5}$' name={`row_${index}.port`} placeholder="Port" value={p.port} onSave={(value) => onRowArrayChange(fieldName, index, "port", sanitizePort(value))} />
              <RMIconButton icon={HiTrash} aria-label="Delete Route" onClick={() => onRowDropIndex(fieldName, index)} />
            </HStack>
          )) : (
            <Text>No saved route mappings</Text>
          )}
      </VStack>
      {formData.length > 0 && (
        <VStack spacing={3} align="stretch">
          <Heading as="h3" size="md">
            New Route Mapping
          </Heading>
          <Text>Enter the route mappings for your deployment. Select a volume directory and specify the source and destination Routes.</Text>
          {formData.map((p, index) => (
            <HStack key={`new_${p.id}`}>
              <Input name={`new_${index}.cname`} placeholder="CNAME" value={p.cname} onChange={(e) => handleArrayChange(index, "cname", e.target.value)} />
              <Input name={`new_${index}.port`} pattern='^[0-9]{1,5}$' placeholder="Port" value={p.port} onChange={(e) => handleArrayChange(index, "port", sanitizePort(e.target.value))} />
              <RMIconButton icon={HiTrash} aria-label="Delete Route" onClick={() => handleRemoveItem(index)} />
            </HStack>
          ))}
        </VStack>
      )}
      <VStack spacing={3} align="stretch">
        <HStack>
          {formData?.length > 0 && (
            <RMButton onClick={handleSaveAdd}>
              Save Route
            </RMButton>
          )}
          <RMButton onClick={handleAddItem}>
            Add Route
          </RMButton>
        </HStack>
      </VStack>
    </Flex>
  )
})
