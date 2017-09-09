// +build server

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/signal18/replication-manager/crypto"
)

var (
	keyPath   string
	overwrite bool
)

func init() {
	rootCmd.AddCommand(keygenCmd)
	rootCmd.AddCommand(passwordCmd)
	keygenCmd.Flags().StringVar(&keyPath, "keypath", "/etc/replication-manager/.replication-manager.key", "Encryption key file path")
	keygenCmd.Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite the previous key")
}

var keygenCmd = &cobra.Command{
	Use:   "keygen",
	Short: "Generate a new encryption key",
	Long: `Generates a new AES128 encryption key that can be used for encrypting cleartext passwords on
the CLI or in the replication-manager config file`,
	Run: func(cmd *cobra.Command, args []string) {
		p := crypto.Password{}
		var err error
		p.Key, err = crypto.Keygen()
		if err != nil {
			log.Fatalln(err)
		}
		err = writeKey(p.Key)
		if err != nil {
			log.Fatalln(err)
		}
	},
}

var passwordCmd = &cobra.Command{
	Use:   "password",
	Short: "Encrypt a clear text password",
	Long:  "Encrypt a clear text password using the AES encryption key generated with the keygen command",
	Run: func(cmd *cobra.Command, args []string) {
		p := crypto.Password{}
		var err error
		p.Key, err = readKey()
		if err != nil {
			log.Fatalln(err)
		}
		p.PlainText = strings.Join(args, " ")
		p.Encrypt()
		fmt.Println("Encrypted password hash:", p.CipherText)
	},
}

func writeKey(key []byte) error {
	if _, err := os.Stat(keyPath); err == nil {
		if !overwrite {
			return errors.New("Key file already exists")
		}
	}

	flag := os.O_WRONLY | os.O_CREATE

	file, err := os.OpenFile(keyPath, flag, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(key)
	return err
}

func readKey() ([]byte, error) {
	flag := os.O_RDONLY
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		return nil, errors.New("Key file does not exist")
	}
	file, err := os.OpenFile(keyPath, flag, 0600)
	if err != nil {
		return nil, err
	}
	key := make([]byte, 16)
	_, err = file.Read(key)
	return key, err
}
