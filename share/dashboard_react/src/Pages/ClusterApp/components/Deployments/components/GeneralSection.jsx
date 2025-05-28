import { Flex, FormControl, FormLabel, VStack } from '@chakra-ui/react'
import TextForm from '../../../../../components/TextForm';
import styles from './styles.module.scss';
const defaultConfirmText = "Are you sure to change this field to: ";

export default function GeneralSection({ row, onChange }) {
  return (
    <Flex direction="column" className={`${styles.sectionWrapper}`}>
      <VStack spacing={3} align="stretch">
        <FormControl>
          <FormLabel>Name</FormLabel>
          <TextForm confirmTitle={defaultConfirmText} isDisabled={true} name="name" value={row.name || ''} onSave={(value) => onChange("name", value)} />
        </FormControl>

        <FormControl>
          <FormLabel>Docker Image</FormLabel>
          <TextForm confirmTitle={defaultConfirmText} name="dockerImg" value={row.dockerImg || ''} onSave={(value) => onChange("dockerImg", value)}
          />
        </FormControl>

        <FormControl>
          <FormLabel>Docker Run Cmd</FormLabel>
          <TextForm confirmTitle={defaultConfirmText} name="dockerRunCmd" value={row.dockerRunCmd || ''} onSave={(value) => onChange("dockerRunCmd", value)} />
        </FormControl>
      </VStack>
    </Flex>
  )
}
