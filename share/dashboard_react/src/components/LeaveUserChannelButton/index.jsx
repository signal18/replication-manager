import React, { useState } from 'react';
import { useDispatch } from 'react-redux';
import { Button, AlertDialog,AlertDialogBody, AlertDialogFooter, AlertDialogHeader, AlertDialogContent, AlertDialogOverlay } from '@chakra-ui/react';
import styles from './styles.module.scss';
import { leaveChannel} from '../../redux/meetSlice';
import { FaArrowRight} from 'react-icons/fa';

const LeaveUserChannelButton = ({ selectedChannel, onSelectChannel }) => {
    const dispatch = useDispatch();
    const [channelToLeave, setChannelToLeave] = useState(null);
    const [isOpen, setIsOpen] = useState(false);

    //Handler to leave channel
    const handleLeaveChannel = (channelId) => {
        setChannelToLeave(channelId);
        setIsOpen(true);
    };

    const confirmLeaveChannel = () => {
        dispatch(leaveChannel({ChannelId: channelToLeave}));
        setIsOpen(false);
        if (onSelectChannel) {
            onSelectChannel('');
        }
    };

    const cancelLeaveChannel = () => {
        setIsOpen(false);
    };

    return (
    <>
        <Button
            colorScheme="red"
            size="sm"
            onClick={(e) => {
                e.stopPropagation();
                handleLeaveChannel(selectedChannel);
            }}
        >
            <FaArrowRight />
        </Button>
        <AlertDialog isOpen={isOpen} leastDestructiveRef={undefined} onClose={cancelLeaveChannel}>
            <AlertDialogOverlay>
                <AlertDialogContent>
                    <AlertDialogHeader fontSize='lg' fontWeight='bold'>
                        Leave Channel
                    </AlertDialogHeader>
                    <AlertDialogBody>
                        Are you sure you want to leave this channel?
                    </AlertDialogBody>
                    <AlertDialogFooter>
                        <Button onClick={cancelLeaveChannel}>
                            Cancel
                        </Button>
                        <Button colorScheme='red' onClick={confirmLeaveChannel} ml={3}>
                            Leave
                        </Button>
                    </AlertDialogFooter>
                </AlertDialogContent>
            </AlertDialogOverlay>
        </AlertDialog>
    </>
    );
};

export default LeaveUserChannelButton;