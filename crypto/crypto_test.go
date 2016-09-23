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
