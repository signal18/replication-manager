// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package crypto

import "testing"

func TestEncryptDecrypt(t *testing.T) {
	varpass := "mypass"
	p := Password{PlainText: varpass}
	var err error
	p.Key, err = Keygen()
	if err != nil {
		t.Fatal(err)
	}
	p.Encrypt()
	t.Log("Encrypted password is", p.CipherText)
	p.PlainText = ""
	p.Decrypt()
	if p.PlainText != varpass {
		t.Fatalf("Decrypted password %s differs from initial password", p.PlainText)
	}
}
