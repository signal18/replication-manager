import React, { useEffect, useRef } from "react";
import { Avatar, Flex, Text } from "@chakra-ui/react";

const ChatChannels = ({ channels }) => {
  return (
    <Flex w="100%" overflowY="scroll" flexDirection="column" p="3">
      {channels.map((item, index) => {
          return (
            <Flex key={index} w="100%" justify="flex-end">
              <Flex
                color="white"
                minW="100px"
                maxW="350px"
                my="1"
                p="3"
              >
                <Text>{item.name}</Text>
              </Flex>
            </Flex>
          );
      })}
    </Flex>
  );
};

export default ChatChannels;
