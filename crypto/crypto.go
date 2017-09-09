// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log"
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

func (p *Password) Decrypt() {
	ciphertext, _ := hex.DecodeString(p.CipherText)

	block, err := aes.NewCipher(p.Key)
	if err != nil {
		log.Println("ERROR: Could not get new cipher:", err)
		return
	}

	if len(ciphertext) < aes.BlockSize {
		log.Println("ERROR: ciphertext too short")
		return
	}
	iv := ciphertext[:aes.BlockSize]
	ciphertext = ciphertext[aes.BlockSize:]

	stream := cipher.NewCFBDecrypter(block, iv)

	stream.XORKeyStream(ciphertext, ciphertext)
	p.PlainText = string(ciphertext)
	return
}
