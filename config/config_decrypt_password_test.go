package config

import "testing"

func TestGetDecryptedPasswordNotACiphertext(t *testing.T) {
	conf := &Config{SecretKey: []byte("0123456789abcdef")}
	for _, v := range []string{"hash_abc", "hash_mypass"} {
		if got := conf.GetDecryptedPassword("k", v); got != v {
			t.Errorf("GetDecryptedPassword(%q) = %q, want the value unchanged", v, got)
		}
	}
}
