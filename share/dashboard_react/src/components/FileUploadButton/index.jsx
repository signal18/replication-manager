import React, { useRef } from 'react';
import styles from './styles.module.scss';
import { Input, Button } from '@chakra-ui/react';
import { FaPaperclip } from 'react-icons/fa';

const FileUploadButton = ({ onFileSelected }) => {
    const fileInputRef = useRef(null);

    const handleButtonClick = () => {
        if (fileInputRef.current) {
            fileInputRef.current.click();
        } else {
            console.error('File input ref is null');
        }
    };

    const handleFileChange = (event) => {
        const file = event.target.files[0];
        if (file) {
            onFileSelected(file);
        }
    };

    return (
        <>
            <Input
                type="file"
                ref={fileInputRef}
                onChange={handleFileChange}
                style={{ display: 'none' }} // Hide the file input
            />
            <Button onClick={handleButtonClick} className={styles.attachFileButton}>
                <FaPaperclip className={styles.attachFileButton}/>
            </Button>
        </>
    );
};

export default FileUploadButton;
