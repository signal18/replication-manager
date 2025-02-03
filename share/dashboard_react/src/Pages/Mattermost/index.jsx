import styles from './styles.module.scss';
import React, { useEffect, useState, useRef } from 'react';
import { useDispatch, useSelector } from 'react-redux';
import { Tabs, TabList, TabPanels, Tab, TabPanel, Box, Textarea, Button, Drawer, DrawerBody, DrawerFooter, DrawerHeader, DrawerOverlay, DrawerContent, DrawerCloseButton } from '@chakra-ui/react';
import { getMeetInfo, postMeetMessage, fetchMessages, fetchNewMessages, loadHistoryMessages } from '../../redux/meetSlice';
import ChannelTreeView from '../../components/ChannelTreeView';


function MattermostIntegration({ isOpen, onClose }) {
    if (!isOpen) return null;
    const dispatch = useDispatch();
    const { meetInfo, messages, loading, error } = useSelector((state) => state.meet);
    const [channels, setChannels] = useState([]);
    const [message, setMessage] = useState('');
    const [selectedChannel, setSelectedChannel] = useState('');
    const [page, setPage] = useState(0);
    const messagesContainerRef = useRef(null);
    const [scrollPosition, setScrollPosition] = useState(null);

    //to get the meet info
    useEffect(() => {
        dispatch(getMeetInfo());
    }, [dispatch]);

    //to get the channels from meet info
    useEffect(() => {
        if (meetInfo) {
            const allChannels = [
                ...Object.entries(meetInfo.channel_ids_open).map(([name, id]) => ({ name, id, type: 'O' })),
                ...Object.entries(meetInfo.channel_ids_private).map(([name, id]) => ({ name, id, type: 'P' })),
                ...Object.entries(meetInfo.channel_ids_direct).map(([name, id]) => ({ name, id, type: 'D' })),
            ];
            setChannels(allChannels);
        }
    }, [meetInfo]);

    //to set messages for selected channel for the fist time
    useEffect(() => {
        if (selectedChannel) {
            setPage(0);
            dispatch(fetchMessages({ channelId: selectedChannel, page: 0 }));
        }
    }, [dispatch, selectedChannel]);

    //to handle the scroll position
    useEffect(() => {
        if (!messagesContainerRef.current) return;
    
        if (scrollPosition !== null) {
            // Rétablir la position du scroll après chargement des messages
            messagesContainerRef.current?.scrollTop =
                messagesContainerRef.current?.scrollHeight - scrollPosition;
        } else if (page === 0) {
            // Si c'est le premier chargement, scroll en bas
            messagesContainerRef.current?.scrollTop = messagesContainerRef.current?.scrollHeight;
        }
    }, [messages]);

    //to update messages every 5 seconds
    useEffect(() => {
        const interval = setInterval(() => {
            if (selectedChannel) {
                setScrollPosition(messagesContainerRef.current?.scrollHeight - messagesContainerRef.current?.scrollTop);
                dispatch(fetchNewMessages({ channelId: selectedChannel }));
            }
        }, 5000);

        return () => clearInterval(interval);
    }, [dispatch, selectedChannel]);

    //to handle the scroll event when user reaches the top 
    const handleScroll = () => {
        const container = messagesContainerRef.current;
        if (!container) return;

        if (container.scrollTop === 0 && !loading) {

            setScrollPosition(container.scrollHeight); // to save the scroll position

            setPage((prevPage) => {
                const nextPage = prevPage + 1;
                dispatch(loadHistoryMessages({ channelId: selectedChannel, page: nextPage }));
                return nextPage;
            });
        }
    };

    //to send the message to the selected channel
    const sendMessage = async () => {
        if (selectedChannel && message) {
            await dispatch(postMeetMessage({ channelId: selectedChannel, message }));
            setMessage('');
            // Scroller vers le bas après l'envoi du message
            if (messagesContainerRef.current) {
                messagesContainerRef.current.scrollTop = messagesContainerRef.current.scrollHeight;
            }
        }
    };

    const getUserName = (userId) => {
        return meetInfo.all_users[userId] || userId;
    };

    return (
        <Drawer isOpen={isOpen} placement="right" onClose={onClose} size="lg">
            <DrawerOverlay />
            <DrawerContent>
                <DrawerCloseButton />
                <DrawerHeader>Support</DrawerHeader>

                <DrawerBody>
                    <Box className={styles.flexContainer}>
                        <Box className={styles.treeViewWrapper}>
                            <ChannelTreeView channels={channels} onSelectChannel={setSelectedChannel} unReadMessagesByChannel={meetInfo?.unread_messages_by_channel || {}} />
                        </Box>
                        <Box
                            ref={messagesContainerRef}
                            onScroll={handleScroll}
                            //className={styles.messagesWrapper}
                            style={{
                                overflowY: 'auto',
                                maxHeight: '400px', // Remplace par une valeur adaptée
                                border: '2px solid red', // Pour bien visualiser le conteneur
                            }}
                        >
                            <Box className={styles.messagesContainer}>
                                {messages[selectedChannel] && messages[selectedChannel].slice().reverse().map((msg, index) => (
                                    <Box key={index} className={styles.message}>
                                        <strong>{getUserName(msg.UserId)}</strong>: {msg.Message}
                                    </Box>
                                ))}
                            </Box>
                            <Textarea
                                value={message}
                                onChange={(e) => setMessage(e.target.value)}
                                placeholder="Write a message..."
                                className={styles.messageInput}
                            />
                        </Box>
                    </Box>
                </DrawerBody>

                <DrawerFooter>
                    <Button variant="outline" mr={3} onClick={onClose}>
                        Close
                    </Button>
                    <Button onClick={sendMessage} className={styles.sendButton}>Send</Button>
                </DrawerFooter>
            </DrawerContent>
        </Drawer>
    );
}

export default MattermostIntegration;