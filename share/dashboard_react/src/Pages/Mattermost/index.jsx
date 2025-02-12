import styles from './styles.module.scss';
import React, { useEffect, useState, useRef, memo } from 'react';
import { useDispatch, useSelector, shallowEqual } from 'react-redux';
import { Input, Box, Textarea, Button, Drawer, DrawerBody, DrawerHeader, DrawerOverlay, DrawerContent, DrawerCloseButton, Text } from '@chakra-ui/react';
import { getMeetInfo, postMeetMessage, fetchMessages, fetchNewMessages, loadHistoryMessages, viewMessagesOnChannel, uploadFileOnChannel, downloadFileFromChannel } from '../../redux/meetSlice';
import ChannelTreeView from '../../components/ChannelTreeView';
import FileUploadButton from '../../components/FileUploadButton';
import { FaDownload } from 'react-icons/fa';



const MattermostIntegration = memo(({ isOpen, onClose }) => {
    if (!isOpen) return null;
    const dispatch = useDispatch();
    const { meetInfo, messages, loading, error } = useSelector((state) => state.meet, shallowEqual);
    const [message, setMessage] = useState('');
    const [selectedChannel, setSelectedChannel] = useState('');
    const [page, setPage] = useState(0);
    const messagesContainerRef = useRef(null);
    const [scrollPosition, setScrollPosition] = useState(null);
    const [selectedFile, setSelectedFile] = useState(null);


    //to set messages for selected channel for the fist time
    useEffect(() => {
        if (selectedChannel) {
            setPage(0);
            dispatch(fetchMessages({ channelId: selectedChannel, page: 0 }));
            dispatch(viewMessagesOnChannel({ channelId: selectedChannel }));
        }
    }, [dispatch, selectedChannel]);

    //to handle the scroll position
    useEffect(() => {
        if (!messagesContainerRef.current) return;
    
        if (scrollPosition !== null) {
            // Rétablir la position du scroll après chargement des messages
            messagesContainerRef.current.scrollTop =
                messagesContainerRef.current.scrollHeight - scrollPosition;
        } else if (page === 0) {
            // Si c'est le premier chargement, scroll en bas
            messagesContainerRef.current.scrollTop = messagesContainerRef.current.scrollHeight;
        }
    }, [messages]);

    //to update messages every 3 seconds
    useEffect(() => {
        const interval = setInterval(() => {
            if (selectedChannel) {
                setScrollPosition(messagesContainerRef.current?.scrollHeight - messagesContainerRef.current?.scrollTop);
                dispatch(fetchNewMessages({ channelId: selectedChannel }));
                dispatch(getMeetInfo());
            }
        }, 2000);

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
    const handleSendMessage = async () => {
        if (selectedChannel) {
            if (selectedFile) {
                // If a file is selected, upload the file and send a message with it
                const formData = new FormData();
                formData.append('file', selectedFile);
                formData.append('fileName', selectedFile.name);
                formData.append('message', message);

                await dispatch(uploadFileOnChannel({ channelId: selectedChannel, formData }));

                setSelectedFile(null);
            } else if (message.trim()) {
                // If no file is selected, just send the message
                await dispatch(postMeetMessage({ channelId: selectedChannel, message }));
            }

            setMessage('');
            if (messagesContainerRef.current) {
                messagesContainerRef.current.scrollTop = messagesContainerRef.current.scrollHeight;
            }
        }
    };

    //////////////////////////////////////
    //Messages Render Functions//////////
    const getUserName = (userId) => {
        return meetInfo.all_users[userId] || userId;
    };

    const formatTime = (timestamp) => {
        const date = new Date(timestamp);
        return date.toLocaleTimeString(); // Format the time as a readable string
    };

    const formatDate = (timestamp) => {
        const date = new Date(timestamp);
        return date.toLocaleDateString(); // Format the date as a readable string
    };

    const formatFileSize = (sizeInBytes) => {
        const units = ['Bytes', 'KB', 'MB', 'GB'];
        let size = sizeInBytes;
        let unitIndex = 0;
        while (size >= 1024 && unitIndex < units.length - 1) {
            size /= 1024;
            unitIndex++;
        }
        return `${size.toFixed(1)} ${units[unitIndex]}`;
    };

    const handleFileDownload = async (selectedfileId, selectedFileName) => {
        try {
            const response = await dispatch(downloadFileFromChannel({fileId : selectedfileId}));
            if (response.payload.response.status === 200) {
                //const blob = new Blob([response.data], { type: 'application/octet-stream' });
                const blob = response.payload.response.data;  
                
                if (!(blob instanceof Blob)) {
                    console.error("Le fichier téléchargé n'est pas un Blob !");
                    return;
                }
                const url = window.URL.createObjectURL(blob);
                const a = document.createElement('a');
                a.style.display = 'none';
                a.href = url;
                a.download = selectedFileName; // Remplacez par le nom réel du fichier
                document.body.appendChild(a);
                a.click();
                window.URL.revokeObjectURL(url);
            } else {
                console.error('Error downloading file');
            }
        } catch (error) {
            console.error('Error downloading file:', error);
        }
    };

    const renderMessages = () => {
        if (!messages[selectedChannel]) return null;

        let lastDate = '';

        return messages[selectedChannel].slice().reverse().map((msg, index) => {
            const messageDate = formatDate(msg.CreateAt);
            const shouldShowDate = messageDate !== lastDate;

            if (shouldShowDate) {
                lastDate = messageDate;
            }

            return (
                <React.Fragment key={index}>
                    {shouldShowDate && (
                        <Box className={styles.dateSeparator}>
                            {messageDate}
                        </Box>
                    )}
                    <Box key={index} className={styles.post}>
                        <div className={styles.postUser}>{getUserName(msg.UserId)} {formatTime(msg.CreateAt)}</div>
                        <div className={styles.postContent}>{msg.Message}</div>
                        {msg?.Metadata && msg?.Metadata.length > 0 && (
                            <Box className={styles.fileAttachments}>
                            {msg.Metadata.map((fileInfo, fileIndex) => (
                                <Box key={fileIndex} className={styles.fileAttachment}>
                                    <Box className={styles.fileDetails}>
                                            <Text className={styles.fileName}>{fileInfo.Name || 'File'}</Text>
                                            <Text className={styles.fileSize}>{formatFileSize(fileInfo.Size)}</Text>
                                    </Box>
                                    <Box className={styles.fileDownload}>
                                        <a href="#" onClick={() => handleFileDownload(fileInfo.ID, fileInfo.Name)} className={styles.fileLink}>
                                            <FaDownload />
                                        </a>
                                    </Box>
                                </Box>
                                ))}
                            </Box>
                        )}
                    </Box>
                </React.Fragment>
            );
        });
    };
    //////////////////////////////////

    /////////////////////////////////
    //File Upload Button Component//

    const handleFileSelected = (file) => {
        console.log('File selected in parent:', file);
        setSelectedFile(file);
    };
    /////////////////////////////////

    return (
        <Drawer isOpen={isOpen} placement="right" onClose={onClose} size="lg">
            <DrawerOverlay />
            <DrawerContent className={styles.mattermostDrawerContent}> 
                <DrawerCloseButton />
                <DrawerHeader className={styles.mattermostChatHeader}>Support</DrawerHeader>

                <DrawerBody className={styles.mattermostContainer} >
                    <Box className={styles.accordionPanel}>
                        <Box className={styles.treeViewWrapper}>
                            <ChannelTreeView onSelectChannel={setSelectedChannel} unReadMessagesByChannel={meetInfo?.unread_messages_by_channel || {}} allUsers={meetInfo?.all_users || {}} />
                        </Box>
                        <Box
                            className={styles.messagesWrapper}
                        >
                            <Box className={styles.messagesContainer} ref={messagesContainerRef} onScroll={handleScroll}>
                                {renderMessages()}
                            </Box>

                            <Box className={styles.newPost}>
                                <Textarea
                                    value={message}
                                    onChange={(e) => setMessage(e.target.value)}
                                    placeholder="Write a message..."
                                    className={styles.newPostInput}
                                />
                                <Button onClick={handleSendMessage} className={styles.newPostSendButton}>
                                    <svg xmlns="http://www.w3.org/2000/svg" version="1.1" width="18" height="18" fill="currentColor" viewBox="0 0 24 24">
                                        <path d="M2,21L23,12L2,3V10L17,12L2,14V21Z"></path>
                                    </svg>
                                </Button>
                                <Box>
                                    <FileUploadButton onFileSelected={handleFileSelected} />
                                    {selectedFile && (
                                        <>
                                        <Text>Fichier sélectionné: {selectedFile.name}</Text>
                                        </>
                                    )}
                                </Box>
                            </Box>
                        </Box>
                    </Box>
                </DrawerBody>
            </DrawerContent>
        </Drawer>
    
    );
});

export default MattermostIntegration;