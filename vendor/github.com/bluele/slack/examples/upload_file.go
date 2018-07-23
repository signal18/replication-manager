package main

import (
	"fmt"
	"path/filepath"

	"github.com/bluele/slack"
)

// Please change these values to suit your environment
const (
	token          = "your-api-token"
	channelName    = "general"
	uploadFilePath = "./assets/test.txt"
)

func main() {
	api := slack.New(token)
	channel, err := api.FindChannelByName(channelName)
	if err != nil {
		panic(err)
	}

	info, err := api.FilesUpload(&slack.FilesUploadOpt{
		Filepath: uploadFilePath,
		Filetype: "text",
		Filename: filepath.Base(uploadFilePath),
		Title:    "upload test",
		Channels: []string{channel.Id},
	})

	if err != nil {
		panic(err)
	}

	fmt.Println(fmt.Sprintf("Completed file upload with the ID: '%s'.", info.ID))
}
