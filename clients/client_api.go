//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package clients

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/spf13/cobra"
)

var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "Call JWT API",
	Long:  `Performs call to jwt api served by monitoring`,
	Run: func(cmd *cobra.Command, args []string) {
		cliInit(false)
		res, err := cliAPICmd(cliUrl, nil)
		if err != nil {
			log.Fatal("Error in API call")
		} else {
			fmt.Printf(res)
		}
	},
	PostRun: func(cmd *cobra.Command, args []string) {
	},
}

func cliAPICmd(urlpost string, params []RequetParam) (string, error) {
	//var r string
	var bearer = "Bearer " + cliToken
	var err error
	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return "", err
	}
	//	ctx, _ := context.WithTimeout(context.Background(), 600*time.Second)
	req.Header.Set("Authorization", bearer)
	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR", err)
		return "", err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR", err)
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", errors.New(string(body))
	}

	return string(body), nil
}
