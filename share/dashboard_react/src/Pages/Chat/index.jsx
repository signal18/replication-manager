import { Divider, Flex } from "@chakra-ui/react";
import { useState } from "react";
import ChatHeader from "../../components/ChatBox/Header";
import ChatMessages from "../../components/ChatBox/Messages";
import ChatFooter from "../../components/ChatBox/Footer";
import ChatChannels from "../../components/ChatBox/Channels";

const Chat = () => {
    const [channels] = useState([
        {
            id: "string",
            create_at: 0,
            update_at: 0,
            delete_at: 0,
            team_id: "string",
            type: "string",
            display_name: "string",
            name: "string",
            header: "string",
            purpose: "string",
            last_post_at: 0,
            total_msg_count: 0,
            extra_update_at: 0,
            creator_id: "string"
        }
    ])
    const [messages] = useState([
        {
            "id": "string",
            "create_at": 0,
            "update_at": 0,
            "delete_at": 0,
            "edit_at": 0,
            "user_id": "string",
            "channel_id": "string",
            "root_id": "string",
            "original_id": "string",
            "message": "string",
            "type": "string",
            "props": {},
            "hashtag": "string",
            "file_ids": [
                "string"
            ],
            "pending_post_id": "string",
            "metadata": {
                "embeds": [
                    {
                        "type": "image",
                        "url": "string",
                        "data": {}
                    }
                ],
                "emojis": [
                    {
                        "id": "string",
                        "creator_id": "string",
                        "name": "string",
                        "create_at": 0,
                        "update_at": 0,
                        "delete_at": 0
                    }
                ],
                "files": [
                    {
                        "id": "string",
                        "user_id": "string",
                        "post_id": "string",
                        "create_at": 0,
                        "update_at": 0,
                        "delete_at": 0,
                        "name": "string",
                        "extension": "string",
                        "size": 0,
                        "mime_type": "string",
                        "width": 0,
                        "height": 0,
                        "has_preview_image": true
                    }
                ],
                "images": {},
                "reactions": [
                    {
                        "user_id": "string",
                        "post_id": "string",
                        "emoji_name": "string",
                        "create_at": 0
                    }
                ],
                "priority": {
                    "priority": "string",
                    "requested_ack": true
                },
                "acknowledgements": [
                    {
                        "user_id": "string",
                        "post_id": "string",
                        "acknowledged_at": 0
                    }
                ]
            }
        }
    ]);
    const [inputMessage, setInputMessage] = useState("");

    const handleSendMessage = () => {
        if (!inputMessage.trim().length) {
            return;
        }
        // Send to mattermost
        //

        // Clear input box
        setInputMessage("");
    };

    return (
        <Flex justify="center" align="center">
            <Flex flexDir="column">
                <ChatChannels channels={channels} />
            </Flex>
            <Flex flexGrow={1} flexDir="column">
                <ChatHeader />
                <Divider />
                <ChatMessages messages={messages} />
                <Divider />
                <ChatFooter
                    inputMessage={inputMessage}
                    setInputMessage={setInputMessage}
                    handleSendMessage={handleSendMessage}
                />
            </Flex>
        </Flex>
    );
};

export default Chat;
