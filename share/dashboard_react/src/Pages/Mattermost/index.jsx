import styles from './styles.module.scss';
import React, { useEffect, useState, useRef } from 'react';
import { useDispatch, useSelector } from 'react-redux';
import { Tabs, TabList, TabPanels, Tab, TabPanel, Box, Textarea, Button, Drawer, DrawerBody, DrawerFooter, DrawerHeader, DrawerOverlay, DrawerContent, DrawerCloseButton } from '@chakra-ui/react';
import { getMeetInfo, readMeetMessages, postMeetMessage } from '../../redux/meetSlice';
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

    useEffect(() => {
        dispatch(getMeetInfo());
    }, [dispatch]);

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


    useEffect(() => {
        if (selectedChannel) {
            setPage(0); 
            dispatch(readMeetMessages({ channelId: selectedChannel, page: 0 }));
        }
    }, [dispatch, selectedChannel]);

    useEffect(() => {
        if (messagesContainerRef.current) {
            if (page === 0) {
                // Premier chargement → on scrolle tout en bas
                messagesContainerRef.current.scrollTop = messagesContainerRef.current.scrollHeight;
            } else if (scrollPosition !== null) {
                // Scroll vers l'ancienne position après ajout de messages
                messagesContainerRef.current.scrollTop = 
                    messagesContainerRef.current.scrollHeight - scrollPosition;
            }
        }
    }, [messages, page]);

    const handleScroll = () => {
        const container = messagesContainerRef.current;
        if (!container) return;
    
        console.log('Scroll position:', container.scrollTop, 'Page:', page);
        
        if (container.scrollTop === 0 && !loading) {
            console.log('Reached top, loading more messages (page', page + 1, ')');
    
            setScrollPosition(container.scrollHeight); // Sauvegarder la position actuelle
            
            setPage((prevPage) => {
                const nextPage = prevPage + 1;
                dispatch(readMeetMessages({ channelId: selectedChannel, page: nextPage }));
                return nextPage;
            });
        }
    };

    const sendMessage = async () => {
        if (selectedChannel && message) {
            await dispatch(postMeetMessage({ channelId: selectedChannel, message }));
            setMessage('');
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