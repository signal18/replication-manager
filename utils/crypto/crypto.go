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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
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

func GetSHA256Hash(text string) string {
	hasher := sha256.New()
	hasher.Write([]byte(text))
	return hex.EncodeToString(hasher.Sum(nil))
}

// GenerateChecksum computes the SHA256 checksum of a file and returns it as a hex string.
func GenerateChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("could not open file %s: %w", filePath, err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("could not compute checksum for file %s: %w", filePath, err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ChecksumDirectory processes each file in the directory and recursively processes subdirectories.
// It generates a checksums.txt file containing the checksums of files and directories.
// It does not include files within subdirectories in the directory's checksum.
func ChecksumDirectory(dir string) (string, error) {
	checksumsFilePath := filepath.Join(dir, "checksums.txt")
	checksumsFile, err := os.Create(checksumsFilePath)
	if err != nil {
		return "", fmt.Errorf("could not create checksums file %s: %w", checksumsFilePath, err)
	}
	defer checksumsFile.Close()

	hash := sha256.New()
	subDirChecksums := make(map[string]string)

	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		// Skip if there's an error or if it's the checksums.txt file itself
		if err != nil || path == checksumsFilePath {
			return err
		}

		// If it's a directory (and not the root directory), process recursively
		if info.IsDir() && path != dir {
			subdirChecksum, err := ChecksumDirectory(path)
			if err != nil {
				return err
			}
			subDirChecksums[path] = subdirChecksum
			return nil
		}

		// If it's a file, compute the checksum
		if !info.IsDir() {
			fileChecksum, err := GenerateChecksum(path)
			if err != nil {
				return err
			}
			relativePath, _ := filepath.Rel(dir, path)
			fmt.Fprintf(checksumsFile, "%s  %s\n", fileChecksum, relativePath)
			hash.Write([]byte(fileChecksum))
		}
		return nil
	})

	if err != nil {
		return "", err
	}

	// Write checksums of subdirectories
	for subDir, checksum := range subDirChecksums {
		_, base := filepath.Split(subDir)
		fmt.Fprintf(checksumsFile, "%s  %s\n", checksum, base)
	}

	// Return the checksum of the entire directory
	return hex.EncodeToString(hash.Sum(nil)), nil
}
