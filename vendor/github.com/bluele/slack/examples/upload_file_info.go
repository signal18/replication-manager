package main

import (
	"fmt"

	"github.com/bluele/slack"
)

// Please change these values to suit your environment
const (
	token          = "your-api-token"
	uploadedFileId = "previous-uploaded-file-id"
)

func main() {
	api := slack.New(token)
	file, err := api.FindFile(uploadedFileId)

	if err != nil {
		panic(err)
	}

	fmt.Println("Information for uploaded file retrieved!")
	fmt.Println(file)
}
