package misc

import (
	"io"
	"net/http"
	"os"
	"time"

	"github.com/siddontang/go/log"
)

func DownloadFile(filepath string, url string) error {

	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
}

func DownloadFileTimeout(url string, file string, timesecond int) error {
	client := http.Client{
		Timeout: time.Duration(timesecond) * time.Second,
	}
	response, err := client.Get(url)
	if err != nil {
		log.Errorf("Get File %s to %s : %s", url, file, err)
		return err
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		log.Errorf("Read File %s to %s : %s", url, file, err)
		return err
	}

	err = os.WriteFile(file, contents, 0644)
	if err != nil {
		log.Errorf("Write File %s to %s : %s", url, file, err)
		return err
	}
	return nil
}
