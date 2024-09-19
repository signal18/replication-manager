// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package crypto

import (
	"bufio"
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
	"sort"
	"strings"
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
// It skips lines that start with '#' (considered comments) and computes the checksum of the remaining content.
func GenerateChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("could not open file %s: %w", filePath, err)
	}
	defer file.Close()

	hash := sha256.New()
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		// Skip lines that start with '#' (considered comments)
		if strings.HasPrefix(line, "#") {
			continue
		}

		// Write the line content to the hash object
		if _, err := hash.Write([]byte(line + "\n")); err != nil {
			return "", fmt.Errorf("could not compute checksum for file %s: %w", filePath, err)
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("could not read file %s: %w", filePath, err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func ChecksumDirectory(dir string, multilevel bool) (string, error) {
	checksumsFilePath := filepath.Join(dir, "checksums.txt")
	checksumsFile, err := os.Create(checksumsFilePath)
	if err != nil {
		return "", fmt.Errorf("could not create checksums file %s: %w", checksumsFilePath, err)
	}
	defer checksumsFile.Close()

	hash := sha256.New()

	// Use a slice to collect all entries and ensure consistent sorting
	var entries []string

	if multilevel {
		err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			// Skip if there's an error or if it's the checksums.txt file itself
			if err != nil {
				return err
			}

			// Skip checksums.txt
			if entry.Name() == "checksums.txt" {
				return nil
			}

			relativePath, _ := filepath.Rel(dir, path)

			// Only process files and directories in the current level
			if strings.Count(relativePath, string(os.PathSeparator)) > 0 {
				// Skip subdirectories and files in subdirectories
				return nil
			}

			entryChecksum := ""

			if entry.IsDir() {
				// Generate checksum for subdirectory
				entryChecksum, err = ChecksumDirectory(path, multilevel)
				if err != nil {
					return err
				}
			} else {
				// Generate checksum for file
				entryChecksum, err = GenerateChecksum(path)
				if err != nil {
					return err
				}
			}

			// Write the checksum and the relative path to the checksums.txt file
			fmt.Fprintf(checksumsFile, "%s  %s\n", entryChecksum, relativePath)

			// Add the entry's checksum to the hash for the directory
			hash.Write([]byte(entryChecksum))

			return nil
		})
	} else {
		err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return fmt.Errorf("error walking directory %s: %w", path, err)
			}

			// Skip the checksums.txt file itself
			if entry.Name() == "checksums.txt" {
				return nil
			}

			// Add relative path to the list of entries
			relativePath, err := filepath.Rel(dir, path)
			if err != nil {
				return fmt.Errorf("error getting relative path for %s: %w", path, err)
			}

			entries = append(entries, relativePath)
			return nil
		})

		if err != nil {
			return "", fmt.Errorf("error traversing directory %s: %w", dir, err)
		}

		// Sort the entries
		sort.Strings(entries)

		// Process each entry
		for _, relativePath := range entries {
			fullPath := filepath.Join(dir, relativePath)
			entryInfo, err := os.Lstat(fullPath)
			if err != nil {
				return "", fmt.Errorf("error getting file info for %s: %w", fullPath, err)
			}

			var entryChecksum string

			if entryInfo.IsDir() {
				// Calculate checksum for the directory
				entryChecksum, err = CalculateDirChecksum(fullPath)
				if err != nil {
					return "", fmt.Errorf("error calculating checksum for directory %s: %w", fullPath, err)
				}
			} else {
				// Calculate checksum for the file
				entryChecksum, err = GenerateChecksum(fullPath)
				if err != nil {
					return "", fmt.Errorf("error calculating checksum for file %s: %w", fullPath, err)
				}
			}

			// Write the checksum and path to the checksums.txt file
			if _, err := fmt.Fprintf(checksumsFile, "%s  %s\n", entryChecksum, relativePath); err != nil {
				return "", fmt.Errorf("error writing checksum to file for %s: %w", relativePath, err)
			}

			hash.Write([]byte(entryChecksum))
		}

	}

	if err != nil {
		return "", err
	}

	// Return the checksum of the entire directory
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func CalculateDirChecksum(dir string) (string, error) {
	hash := sha256.New()

	var entries []string

	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("error walking directory %s: %w", path, err)
		}

		if path == dir {
			return nil
		}

		relativePath, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("error getting relative path for %s: %w", path, err)
		}

		entries = append(entries, relativePath)
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("error traversing directory %s: %w", dir, err)
	}

	sort.Strings(entries)

	for _, relativePath := range entries {
		fullPath := filepath.Join(dir, relativePath)
		entryInfo, err := os.Lstat(fullPath)
		if err != nil {
			return "", fmt.Errorf("error getting file info for %s: %w", fullPath, err)
		}

		var entryChecksum string

		if entryInfo.IsDir() {
			entryChecksum, err = CalculateDirChecksum(fullPath)
			if err != nil {
				return "", fmt.Errorf("error calculating checksum for subdirectory %s: %w", fullPath, err)
			}
		} else {
			entryChecksum, err = GenerateChecksum(fullPath)
			if err != nil {
				return "", fmt.Errorf("error calculating checksum for file %s: %w", fullPath, err)
			}
		}

		hash.Write([]byte(entryChecksum))
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
