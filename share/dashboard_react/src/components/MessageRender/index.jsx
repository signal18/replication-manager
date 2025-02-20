import React from 'react';
import { Box, Text } from '@chakra-ui/react';
import { useDispatch } from 'react-redux';
import styles from './styles.module.scss';
import {downloadFileFromChannel } from '../../redux/meetSlice';
import { FaDownload } from 'react-icons/fa';
import Emoji from 'react-emoji-render';

const MessageRender = ({messages, allUsers}) => {
    const dispatch = useDispatch();
    if (!messages) return null;

    //to get user name from userId
    const getUserName = (userId) => {
        return allUsers[userId] || userId;
    };

    //to format time to get time in readable format for message
    const formatTime = (timestamp) => {
        const date = new Date(timestamp);
        return date.toLocaleTimeString(); // Format the time as a readable string
    };

    //to format date to get date in readable format for message
    const formatDate = (timestamp) => {
        const date = new Date(timestamp);
        return date.toLocaleDateString(); // Format the date as a readable string
    };

    //to format file size in readable format
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

    //to handle file download
    const handleFileDownload = async (selectedfileId, selectedFileName) => {
        try {
            const response = await dispatch(downloadFileFromChannel({fileId : selectedfileId}));
            if (response.payload.response.status === 200) {
                const blob = response.payload.response.data;  
                
                if (!(blob instanceof Blob)) {
                    console.error("Download file is not a Blob !");
                    return;
                }
                const url = window.URL.createObjectURL(blob);
                const a = document.createElement('a');
                a.style.display = 'none';
                a.href = url;
                a.download = selectedFileName;
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


    let lastDate = '';

    return messages.slice().reverse().map((msg, index) => {
        const messageDate = formatDate(msg.CreateAt);
        const shouldShowDate = messageDate !== lastDate;

        if (shouldShowDate) {
            lastDate = messageDate;
        }

        return (
            <React.Fragment key={index} className={styles.messagesRender}>
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

export default MessageRender;