// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-git/go-git/v5"
	git_https "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/githelper"
)

// An instance registered through Cloud18 (#2159) keeps one GitLab project access token per
// config repository (git-acces-token for git-url, git-pull-access-token for git-url-pull) and
// renews them itself before they end (self-rotation): no password, no realm, no CRM after the
// registration. Its project ids say it was registered that way; an instance registered with
// the GitLab password keeps its personal token and the password path.

const (
	// Cloud18GitTokenLifetime is how long a project token is asked for (GitLab caps at a year).
	Cloud18GitTokenLifetime = 364 * 24 * time.Hour
	// cloud18GitTokenRenewBefore: the tokens are rotated when they end within this.
	cloud18GitTokenRenewBefore = 30 * 24 * time.Hour
)

// ErrCloud18GitTokenLost: GitLab refuses the instance's project token (expired, revoked): it
// cannot renew itself, the instance must be registered again (a reconnect).
var ErrCloud18GitTokenLost = errors.New("Cloud18 git: GitLab refuses this instance's token: register it again through Cloud18 (it reconnects to its repositories)")

// cloud18GitLost: the instances (working directories) whose project token GitLab refused.
var cloud18GitLost sync.Map

// Cloud18GitTokenLost says GitLab refused this instance's project token at the last rotation:
// the request to register again stays until a rotation or a registration succeeds.
func (conf *Config) Cloud18GitTokenLost() bool {
	_, lost := cloud18GitLost.Load(conf.WorkingDir)
	return lost
}

// Cloud18GitProjects says the instance was registered through Cloud18 (project tokens).
func (conf *Config) Cloud18GitProjects() bool {
	return conf.Cloud18GitProjectID > 0
}

// GitPullToken is the token of git-url-pull: its own project token when it has one.
func (conf *Config) GitPullToken() string {
	if t := conf.GetDecryptedValue("git-pull-access-token"); t != "" {
		return t
	}
	return conf.GetDecryptedValue("git-acces-token")
}

// Cloud18GitTokensDue says the project tokens end within cloud18GitTokenRenewBefore (or their
// end is unknown).
func (conf *Config) Cloud18GitTokensDue(now time.Time) bool {
	if !conf.Cloud18GitProjects() {
		return false
	}
	end, err := time.Parse("2006-01-02", conf.Cloud18GitTokenExpires)
	return err != nil || end.Sub(now) < cloud18GitTokenRenewBefore
}

// keepSecret sets a secret and the dynamic config entry that saves it (encrypted).
func (conf *Config) keepSecret(key, value string) {
	s := conf.Secrets.Get(key)
	s.OldValue, s.Value = s.Value, value
	conf.Secrets.Set(key, s)
	conf.DeleteImmuableFlag(key)
	conf.SetDynamicFlag(key, conf.EncryptSecretValue(value))
}

// SetCloud18GitTokens keeps the instance's project tokens and their end. The caller saves the
// config and gives the clusters the new secrets.
func (conf *Config) SetCloud18GitTokens(main, pull string, expires string) {
	conf.GitAccesToken, conf.GitPullAccesToken = main, pull
	conf.keepSecret("git-acces-token", main)
	conf.keepSecret("git-pull-access-token", pull)
	conf.Cloud18GitTokenExpires = expires
	conf.SetDynamicFlag("cloud18-git-token-expires", expires)
}

// RotateCloud18GitTokens renews both project tokens with themselves. A token GitLab refuses
// returns ErrCloud18GitTokenLost; any other error is transient (tried again later). The pull
// token is renewed only once the main one is, so a failure leaves at most a fresh main token
// with the old, still valid, pull token.
func (conf *Config) RotateCloud18GitTokens(now time.Time) (err error) {
	if !conf.Cloud18GitProjects() {
		return nil
	}
	expires := now.Add(Cloud18GitTokenLifetime)
	lost := func(err error) error {
		if s := githelper.GitLabStatus(err); s == http.StatusUnauthorized || s == http.StatusForbidden {
			return fmt.Errorf("%w (%v)", ErrCloud18GitTokenLost, err)
		}
		return err
	}
	defer func() {
		if errors.Is(err, ErrCloud18GitTokenLost) {
			cloud18GitLost.Store(conf.WorkingDir, true)
		} else if err == nil {
			cloud18GitLost.Delete(conf.WorkingDir)
		}
	}()
	main, err := githelper.GitLabRotateProjectToken(conf.GetDecryptedValue("git-acces-token"), conf.Cloud18GitProjectID, expires)
	if err != nil {
		return lost(err)
	}
	pull := conf.GetDecryptedValue("git-pull-access-token")
	if conf.Cloud18GitPullProjectID > 0 {
		p, perr := githelper.GitLabRotateProjectToken(pull, conf.Cloud18GitPullProjectID, expires)
		if err = perr; err != nil {
			conf.SetCloud18GitTokens(main.Token, pull, conf.Cloud18GitTokenExpires)
			return lost(err)
		}
		pull = p.Token
	}
	end := main.ExpiresAt
	if end == "" {
		end = expires.Format("2006-01-02")
	}
	conf.SetCloud18GitTokens(main.Token, pull, end)
	return nil
}

// OwnsEncryptedValues says whether this instance's encryption key decrypts the encrypted values
// (hash_…) of a saved config: AES-CFB has no integrity check, a wrong key gives random bytes, so
// every value of 8 bytes or more must come back as text (valid UTF-8, no control character but
// tab and line breaks: a key file or an accented password is text), and at least one must be
// there. With a single 8-byte value, random bytes pass about once in a few thousand: the domain
// membership checked first is the real gate.
// Used to tell a reconnect (same instance or a peer: they share the key, never in git) from
// another instance's URI.
func (conf *Config) OwnsEncryptedValues(values []string) bool {
	key := conf.cloud18EncryptionKey()
	checked := 0
	for _, v := range values {
		if !strings.HasPrefix(v, "hash_") || len(v) < len("hash_")+2*(16+8) {
			continue
		}
		p := crypto.Password{Key: key, CipherText: strings.TrimPrefix(v, "hash_")}
		if key == nil || p.Decrypt() != nil || !printable(p.PlainText) {
			return false
		}
		checked++
	}
	return checked > 0
}

func printable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if (r < 0x20 && r != '\n' && r != '\r' && r != '\t') || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return false
		}
	}
	return true
}

// GitHeadPushed says whether the remote "origin" of the git working directory dir has its current
// commit (the HEAD's branch on the remote points to it): a request repman pushed has reached GitLab.
func (conf *Config) GitHeadPushed(dir, user, token string) (bool, error) {
	r, err := git.PlainOpen(dir)
	if err != nil {
		return false, err
	}
	head, err := r.Head()
	if err != nil {
		return false, err
	}
	remote, err := r.Remote("origin")
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitNetworkTimeout)
	defer cancel()
	opts := &git.ListOptions{}
	if token != "" {
		opts.Auth = &git_https.BasicAuth{Username: user, Password: token}
	}
	refs, err := remote.ListContext(ctx, opts)
	if err != nil {
		return false, err
	}
	for _, ref := range refs {
		if ref.Name() == head.Name() {
			return ref.Hash() == head.Hash(), nil
		}
	}
	return false, nil
}
