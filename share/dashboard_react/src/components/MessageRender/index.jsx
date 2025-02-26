import React from 'react';
import { Box, Text, Button } from '@chakra-ui/react';
import { useDispatch } from 'react-redux';
import styles from './styles.module.scss';
import {downloadFileFromChannel } from '../../redux/meetSlice';
import { FaDownload} from 'react-icons/fa';
import { Divider} from '@chakra-ui/react'

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
            <React.Fragment key={index} >
                <Box className={styles.messagesRender}>
                    {shouldShowDate && (
                        <Box className={styles.dateSeparator}>
                            <Divider className={styles.dateSeparatorLine}/>
                            <Box className={styles.dateSeparatorDate}>
                                {messageDate}
                            </Box>
                            <Divider className={styles.dateSeparatorLine}/>
                        </Box>
                    )}
                    <Box key={index} className={styles.post}>
                        <div className={styles.postInfo}>
                            <div className={styles.postUser}>
                                { msg.OverriveUserName !== ""? msg.OverriveUserName: getUserName(msg.UserId) }
                            </div>
                            <div className={styles.postTime}>
                                {formatTime(msg.CreateAt)}
                            </div>
                        </div>
                        <div className={styles.postContent}>{msg.Message}</div>
                        {msg?.FileMetadata && msg?.FileMetadata.length > 0 && (
                            <Box className={styles.fileAttachments}>
                            {msg.FileMetadata.map((fileInfo, fileIndex) => (
                                <Box key={fileIndex} className={styles.fileAttachment}>
                                    <Box className={styles.fileLogo}>
                                        {fileInfo.Extension == "pdf" && (
                                            <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" stroke="#ff0000">
                                                <g id="SVGRepo_bgCarrier" stroke-width="0"></g>
                                                <g id="SVGRepo_tracerCarrier" stroke-linecap="round" stroke-linejoin="round"></g>
                                                <g id="SVGRepo_iconCarrier"> 
                                                    <path d="M4 4C4 3.44772 4.44772 3 5 3H14H14.5858C14.851 3 15.1054 3.10536 15.2929 3.29289L19.7071 7.70711C19.8946 7.89464 20 8.149 20 8.41421V20C20 20.5523 19.5523 21 19 21H5C4.44772 21 4 20.5523 4 20V4Z" stroke="#200E32" stroke-width="2" stroke-linecap="round"></path> 
                                                    <path d="M20 8H15V3" stroke="#200E32" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"></path> 
                                                    <path d="M11.5 13H11V17H11.5C12.6046 17 13.5 16.1046 13.5 15C13.5 13.8954 12.6046 13 11.5 13Z" stroke="#200E32" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"></path> 
                                                    <path d="M15.5 17V13L17.5 13" stroke="#200E32" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"></path> 
                                                    <path d="M16 15H17" stroke="#200E32" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"></path>
                                                    <path d="M7 17L7 15.5M7 15.5L7 13L7.75 13C8.44036 13 9 13.5596 9 14.25V14.25C9 14.9404 8.44036 15.5 7.75 15.5H7Z" stroke="#200E32" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"></path> 
                                                </g>
                                            </svg>
                                        )}
                                        {fileInfo.Extension != "pdf" && (
                                            <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
                                                <g id="SVGRepo_bgCarrier" stroke-width="0"></g>
                                                <g id="SVGRepo_tracerCarrier" stroke-linecap="round" stroke-linejoin="round"></g>
                                                <g id="SVGRepo_iconCarrier"> 
                                                    <path d="M4 4C4 3.44772 4.44772 3 5 3H14H14.5858C14.851 3 15.1054 3.10536 15.2929 3.29289L19.7071 7.70711C19.8946 7.89464 20 8.149 20 8.41421V20C20 20.5523 19.5523 21 19 21H5C4.44772 21 4 20.5523 4 20V4Z" stroke="#200E32" stroke-width="2" stroke-linecap="round"></path> <path d="M20 8H15V3" stroke="#200E32" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"></path> 
                                                    <path d="M9 16L15 16" stroke="#200E32" stroke-width="2" stroke-linecap="round"></path>
                                                    <path d="M9 12L15 12" stroke="#200E32" stroke-width="2" stroke-linecap="round"></path>
                                                    <path d="M9 8L11 8" stroke="#200E32" stroke-width="2" stroke-linecap="round"></path>
                                                </g>
                                            </svg>
                                        )}

                                    </Box>
                                    <Box className={styles.fileDetails}>
                                        <div className={styles.fileName}>{fileInfo.Name || 'File'}</div>
                                        <div className={styles.fileSize}>{formatFileSize(fileInfo.Size)}</div>
                                        <Box className={styles.fileDownload}>
                                            <a href="#" onClick={() => handleFileDownload(fileInfo.ID, fileInfo.Name)} className={styles.fileLink}>
                                                <FaDownload />
                                            </a>
                                        </Box>
                                    </Box>
                                </Box>
                                ))}
                            </Box>
                        )}
                        {msg?.AlertMetadata && msg?.AlertMetadata.length > 0 && (
                            <Box className={styles.alerts}>
                                {msg.AlertMetadata.map((alertInfo, alertIndex) => (
                                    <Box className={styles.alert}>
                                        <div className={styles.alertInfoText}>{alertInfo.Text}</div>
                                        {alertInfo?.Fields && alertInfo.Fields.length > 0 && (
                                            <Box 
                                                className={styles.alertFields}
                                                style={{'border-color' : `${alertInfo.Color === "danger" ? 'red' : alertInfo.Color === "warning" ? 'orange': 'green'}`}}
                                            >
                                                <Box 
                                                    className={styles.content} 
                                                >
                                                    {alertInfo.Fields.map((field, fieldIndex) => (
                                                        <Box className={styles.field}>
                                                            <div className={styles.fieldTitle}>{field.Title}</div>
                                                            <div className={styles.fieldContent}>{field?.Value}</div>
                                                        </Box>
                                                    ))}
                                                </Box>
                                            </Box>
                                        )}
                                    </Box>
                                ))}
                            </Box>
                        )}
                        {msg?.MeetingMetadata && msg?.MeetingMetadata.length > 0 && (
                            <Box className={styles.meetings}>
                                {msg.MeetingMetadata.map((meetingInfo, meetingIndex) => (
                                    <Box className={styles.meeting}>
                                        <div className={styles.meetingInfoText}>{meetingInfo?.Text}</div>
                                        {meetingInfo?.MeetLink && (
                                            <Box className={styles.meetingCard}>
                                                <Text className={styles.meetingTitle}> Meeting Jitsi</Text>
                                                <Text className={styles.meetingInfo}>{'Meeting ID : ' + meetingInfo?.ID}</Text>
                                                <Button 
                                                    as="a" 
                                                    href={meetingInfo.MeetLink} 
                                                    target="_blank" 
                                                    rel="noopener noreferrer" 
                                                    className={styles.meetingButton}
                                                >
                                                    Join meeting
                                                </Button>
                                            </Box>
                                        )}
                                    </Box>
                                ))}
                            </Box>
                        )}
                    </Box>
                </Box>
            </React.Fragment>
        );
    });
};

export default MessageRender;