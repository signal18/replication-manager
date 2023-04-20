// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"os"
)

type Password struct {
	Key        []byte
	CipherText string
	PlainText  string
}

func Keygen() ([]byte, error) {
	c := 16
	b := make([]byte, c)
	_, err := rand.Read(b)
	return b, err
}

func (p *Password) Encrypt() {
	plaintext := []byte(p.PlainText)
	block, err := aes.NewCipher(p.Key)
	if err != nil {
		log.Println("ERROR: Could not get new cipher:", err)
		return
	}

	ciphertext := make([]byte, aes.BlockSize+len(p.PlainText))
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		log.Println("ERROR: Could not read ciphertext:", err)
		return
	}

	stream := cipher.NewCFBEncrypter(block, iv)
	stream.XORKeyStream(ciphertext[aes.BlockSize:], plaintext)
	p.CipherText = hex.EncodeToString(ciphertext)
	return
}

func (p *Password) Decrypt() error {
	ciphertext, _ := hex.DecodeString(p.CipherText)

	block, err := aes.NewCipher(p.Key)
	if err != nil {
		log.Println("ERROR: Could not get new cipher:", err)
		return err
	}

	if len(ciphertext) < aes.BlockSize {
		//log.Println("ERROR: ciphertext too short")
		return errors.New("Ciphertext too short")
	}
	iv := ciphertext[:aes.BlockSize]
	ciphertext = ciphertext[aes.BlockSize:]

	stream := cipher.NewCFBDecrypter(block, iv)

	stream.XORKeyStream(ciphertext, ciphertext)
	p.PlainText = string(ciphertext)
	return nil
}

func WriteKey(key []byte, keyPath string, overwrite bool) error {
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

func ReadKey(keyPath string) ([]byte, error) {
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

func GetMD5Hash(text string) string {
	hasher := md5.New()
	hasher.Write([]byte(text))
	return hex.EncodeToString(hasher.Sum(nil))
}
