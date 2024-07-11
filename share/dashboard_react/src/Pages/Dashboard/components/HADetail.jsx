import React, { useState } from 'react'
import Card from '../../../components/Card'
import {
  Box,
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
import { useSelector } from 'react-redux'

function HADetail({ selectedCluster }) {
  const {
    common: { theme, isDesktop }
  } = useSelector((state) => state)
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
        width={isDesktop ? '50%' : '100%'}
        header={
          <>
            <Text>HA</Text>
            <Box ml='auto'>
              <TagPill type='success' text={selectedCluster.topology} />
            </Box>
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
              <Button variant='ghost' onClick={handleSwitch}>
                Yes
              </Button>
            </ModalFooter>
          </ModalContent>
        </Modal>
      )}
    </>
  )
}

export default HADetail
