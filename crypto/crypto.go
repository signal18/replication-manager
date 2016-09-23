package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
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
		fmt.Println("error:", err)
		return
	}

	ciphertext := make([]byte, aes.BlockSize+len(p.PlainText))
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		panic(err)
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
		panic(err)
	}

	// The IV needs to be unique, but not secure. Therefore it's common to
	// include it at the beginning of the ciphertext.
	if len(ciphertext) < aes.BlockSize {
		panic("ciphertext too short")
	}
	iv := ciphertext[:aes.BlockSize]
	ciphertext = ciphertext[aes.BlockSize:]

	stream := cipher.NewCFBDecrypter(block, iv)

	// XORKeyStream can work in-place if the two arguments are the same.
	stream.XORKeyStream(ciphertext, ciphertext)
	p.PlainText = string(ciphertext)
	return
}
