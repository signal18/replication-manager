import React, { useState } from 'react'
import Card from '../../../components/Card'
import {
  Button,
  Modal,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Text
} from '@chakra-ui/react'
import TagPill from '../../../components/TagPill'

function HADetail({ selectedCluster }) {
  const [isModalOpen, setIsModalOpen] = useState(false)
  const [isChecked, setIsChecked] = useState(false)
  const handleSwitchChange = (e) => {
    console.log('valuea;:', e.target.checked)
    setIsModalOpen(true)
  }

  const closeModal = () => {
    setIsModalOpen(false)
  }
  return (
    <>
      <Card
        width={'50%'}
        header={
          <>
            <Text>HA</Text>
            <TagPill type='success' text={selectedCluster.topology} />
          </>
        }
        onSwitchChange={handleSwitchChange}
        showSwitch={true}
      />
      {isModalOpen && (
        <Modal isOpen={isModalOpen} onClose={closeModal}>
          <ModalOverlay />
          <ModalContent>
            <ModalHeader>Confirm switchover?</ModalHeader>
            <ModalCloseButton />

            <ModalFooter>
              <Button colorScheme='blue' mr={3} onClick={closeModal}>
                No
              </Button>
              <Button variant='ghost'>Yes</Button>
            </ModalFooter>
          </ModalContent>
        </Modal>
      )}
    </>
  )
}

export default HADetail
