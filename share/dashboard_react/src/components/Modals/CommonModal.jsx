import {
    Modal,
    ModalBody,
    ModalCloseButton,
    ModalContent,
    ModalHeader,
    ModalOverlay,
} from '@chakra-ui/react'
import React from 'react'
import parentStyles from './styles.module.scss'

function CommonModal({
    body,
    title,
    size = 'md',
    isOpen,
    closeModal,
    contentClassName = '',
    headerClassName = '',
    bodyClassName = '',
    className = ''
}) {
    const bodyClasses = `${bodyClassName || className}`

    return (
        <Modal size={size} isOpen={isOpen} onClose={closeModal}>
            <ModalOverlay />
            <ModalContent className={`${parentStyles.modalLightContent} ${contentClassName}`}>
                <ModalHeader className={headerClassName}>{title}</ModalHeader>
                <ModalCloseButton />
                <ModalBody className={bodyClasses}>
                    {body}
                </ModalBody>
            </ModalContent>
        </Modal>
    )
}

export default CommonModal
