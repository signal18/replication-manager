package server

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	git_obj "github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	git_https "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/githelper"
	log "github.com/sirupsen/logrus"
)

func (repman *ReplicationManager) SetIsGitPush(val bool) {
	repman.IsGitPush = val
	for _, cl := range repman.Clusters {
		cl.IsGitPush = val
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg, "Git push changed: %t", val)
}

func (repman *ReplicationManager) SetIsGitPull(val bool) {
	repman.IsGitPull = val
	for _, cl := range repman.Clusters {
		cl.IsGitPull = val
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg, "Git pull changed: %t", val)
}

func (repman *ReplicationManager) InitGitConfig(conf *config.Config) error {
	if repman.IsGitPush {
		return nil
	}

	repman.SetIsGitPush(true)
	defer repman.SetIsGitPush(false)

	if conf.GitUrl != "" && conf.GitAccesToken != "" && !conf.Cloud18 {
		var tok string

		if conf.IsVaultUsed() && conf.IsPath(conf.GitAccesToken) {
			conn, err := conf.GetVaultConnection()
			if err != nil {
				repman.Logrus.Printf("Error vault connection %v", err)
			}
			tok, err = conf.GetVaultCredentials(conn, conf.GitAccesToken, "git-acces-token")
			if err != nil {
				repman.Logrus.Printf("Error get vault git-acces-token value %v", err)
				tok = conf.GetDecryptedValue("git-acces-token")
			} else {
				var Secrets config.Secret
				Secrets.Value = tok
				conf.Secrets["git-acces-token"] = Secrets
			}

		} else {
			tok = conf.GetDecryptedValue("git-acces-token")
		}

		conf.CloneConfigFromGit(conf.GitUrl, conf.GitUsername, tok, conf.WorkingDir)
	}

	if conf.Cloud18GitUser != "" && conf.Cloud18GitPassword != "" && conf.Cloud18 {
		if conf.Cloud18Domain == "" {
			return fmt.Errorf("Cloud18Domain is empty")
		}

		if conf.Cloud18SubDomain == "" {
			return fmt.Errorf("Cloud18SubDomain is empty")
		}

		if conf.Cloud18SubDomainZone == "" {
			return fmt.Errorf("Cloud18SubDomainZone is empty")
		}

		acces_tok, err := githelper.GetGitLabTokenBasicAuth(conf.Cloud18GitUser, conf.GetDecryptedValue("cloud18-gitlab-password"), conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))
		if err != nil {
			if conf.Verbose || conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlErr) {
				repman.Logrus.Errorf(err.Error() + conf.GetDecryptedValue("cloud18-gitlab-password") + "\n")
			}
			conf.Cloud18 = false
			return err
		}

		repman.SetCloudPartner()

		uid, err := githelper.GetGitLabUserId(acces_tok, conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))
		if err != nil {
			if conf.Verbose || conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlErr) {
				repman.Logrus.Errorf(err.Error() + "\n")
			}
			conf.Cloud18 = false
			return err
		} else if uid == 0 {
			if conf.Verbose || conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlErr) {
				repman.Logrus.Errorf("Invalid user Id \n")
			}
			conf.Cloud18 = false
			return fmt.Errorf("Invalid user Id")
		}

		_, err = githelper.InitGroupAccessLevel(acces_tok, conf.Cloud18Domain, uid, conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))
		if err != nil {
			if conf.Verbose || conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlErr) {
				repman.Logrus.Errorf(err.Error() + "\n")
			}
			conf.Cloud18 = false
			return err
		}

		tokenName := conf.Cloud18Domain + "-" + conf.Cloud18SubDomain + "-" + conf.Cloud18SubDomainZone
		personal_access_token, _ := githelper.GetGitLabTokenOAuth(acces_tok, tokenName, conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))
		if personal_access_token == "" {
			personal_access_token, err = githelper.CreatePersonalAccessTokenCSRF(conf.Cloud18GitUser, conf.GetDecryptedValue("cloud18-gitlab-password"), tokenName)
			if err != nil && (conf.Verbose || conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlErr)) {
				repman.Logrus.Errorf("%v", err.Error())
			}
		}

		if personal_access_token != "" {
			var Secrets config.Secret
			Secrets.Value = personal_access_token
			conf.Secrets["git-acces-token"] = Secrets
			path := conf.Cloud18Domain + "/" + conf.Cloud18SubDomain + "-" + conf.Cloud18SubDomainZone
			name := conf.Cloud18SubDomain + "-" + conf.Cloud18SubDomainZone
			githelper.GitLabCreateProject(personal_access_token, name, path, conf.Cloud18Domain, uid, conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))
			githelper.GitLabCreatePullProject(personal_access_token, name, path, conf.Cloud18Domain, uid, conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))

			conf.GitUrl = conf.OAuthProvider + "/" + conf.Cloud18Domain + "/" + conf.Cloud18SubDomain + "-" + conf.Cloud18SubDomainZone + ".git"
			conf.GitUrlPull = conf.OAuthProvider + "/" + conf.Cloud18Domain + "/" + conf.Cloud18SubDomain + "-" + conf.Cloud18SubDomainZone + "-pull.git"
			conf.GitUsername = conf.Cloud18GitUser
			conf.GitAccesToken = personal_access_token
			conf.ImmuableFlagMap["git-url"] = conf.GitUrl
			conf.ImmuableFlagMap["git-url-pull"] = conf.GitUrlPull
			conf.ImmuableFlagMap["git-username"] = conf.GitUsername
			conf.ImmuableFlagMap["git-acces-token"] = personal_access_token

			if conf.ConfRestoreOnStart {
				conf.ConfRestoreOnStart = false
				conf.ImmuableFlagMap["monitoring-restore-config-on-start"] = false
				os.RemoveAll(conf.WorkingDir)
				conf.CloneConfigFromGit(conf.GitUrl, conf.GitUsername, conf.GitAccesToken, conf.WorkingDir)
				conf.CloneConfigFromGit(conf.GitUrlPull, conf.GitUsername, conf.GitAccesToken, conf.WorkingDir+"/.pull")
			}

		} else if conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlInfo) {
			err := fmt.Errorf("Could not get personal access token from gitlab")
			repman.Logrus.WithField("group", repman.ClusterList[cfgGroupIndex]).Infof(err.Error())
			return err
		}

	}

	return nil
}

func (repman *ReplicationManager) PushAllConfigsToGit() {
	if repman.IsGitPush {
		return
	}

	// Set Flag as Git Push, prevent new cluster save is processed
	repman.SetIsGitPush(true)
	defer repman.SetIsGitPush(false)

	// Wait if any cluster is saving config
	for _, cl := range repman.Clusters {
		for cl.IsSavingConfig {
			time.Sleep(100 * time.Millisecond)
		}
	}

	if repman.Conf.GitUrl == "" {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlInfo, "No Git URL provided, skipping push")
		return
	}
	repman.AddPullToGitignore()
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlInfo, "Pushing All Configs To Git")
	err := repman.PushConfigToGit()
	if err != nil && err == transport.ErrRepositoryNotFound {
		os.RemoveAll(repman.Conf.WorkingDir + "/.git")
		repman.PushConfigToGit()
	}
}

func (repman *ReplicationManager) PullCloud18Configs() {
	if repman.IsGitPull {
		return
	}
	// Set Flag as Git Pull, prevent new cluster save / push is processed
	repman.SetIsGitPull(true)
	defer repman.SetIsGitPull(false)

	// Wait if any cluster is saving config
	for _, cl := range repman.Clusters {
		for cl.IsSavingConfig {
			time.Sleep(100 * time.Millisecond)
		}
	}

	pullDir := repman.Conf.WorkingDir + "/.pull"
	filePath := pullDir + "/cloud18.toml"

	if repman.Conf.GitUrlPull != "" {
		err := repman.Conf.CloneConfigFromGit(repman.Conf.GitUrlPull, repman.Conf.GitUsername, repman.Conf.Secrets["git-acces-token"].Value, pullDir)
		if err != nil {
			os.RemoveAll(pullDir + "/.git")
			repman.Conf.CloneConfigFromGit(repman.Conf.GitUrlPull, repman.Conf.GitUsername, repman.Conf.Secrets["git-acces-token"].Value, pullDir)
		}

		//to check cloud18.toml for the first time
		if repman.Conf.Cloud18 {
			repman.CheckCloud18Config(filePath)
			repman.LoadPeerJson()
			repman.LoadPartnersJson()
		}
	}

	if repman.Conf.Cloud18 {
		//then to check new file pulled in working dir
		files, err := os.ReadDir(repman.Conf.WorkingDir)
		if err != nil {
			repman.Logrus.Infof("No working directory %s ", repman.Conf.WorkingDir)
		}
		//check all dir of the datadir to check if a new cluster has been pull by git
		for _, f := range files {
			new_cluster_discover := true
			if f.IsDir() && f.Name() != "graphite" && f.Name() != "backups" && f.Name() != ".git" && f.Name() != "cloud18.toml" && !strings.Contains(f.Name(), ".json") && !strings.Contains(f.Name(), ".csv") && f.Name() != ".pull" {
				for name, _ := range repman.Clusters {
					if name == f.Name() {
						new_cluster_discover = false
					}
				}
			} else {
				new_cluster_discover = false
			}
			//find a dir that is not in the cluster list (and diff from backups and graphite)
			//so add the to new cluster to the repman
			if new_cluster_discover {
				//check if this there is a config file in the dir
				if _, err := os.Stat(repman.Conf.WorkingDir + "/" + f.Name() + "/" + f.Name() + ".toml"); !os.IsNotExist(err) {
					//init config, start the cluster and add it to the cluster list
					repman.ViperConfig.SetConfigName(f.Name())
					repman.ViperConfig.SetConfigFile(repman.Conf.WorkingDir + "/" + f.Name() + "/" + f.Name() + ".toml")
					err := repman.ViperConfig.MergeInConfig()
					if err != nil {
						repman.Logrus.Errorf("Config error in " + repman.Conf.WorkingDir + "/" + f.Name() + "/" + f.Name() + ".toml" + ":" + err.Error())
					}
					repman.Confs[f.Name()] = repman.GetClusterConfig(repman.ViperConfig, repman.Conf.ImmuableFlagMap, repman.Conf.DynamicFlagMap, f.Name(), repman.Conf)
					repman.StartCluster(f.Name())
					repman.Clusters[f.Name()].IsGitPull = true
					for _, cluster := range repman.Clusters {
						cluster.SetClusterList(repman.Clusters)
					}
					repman.ClusterList = append(repman.ClusterList, f.Name())
				}
			}
		}
	}
}

func (repman *ReplicationManager) ReadCloud18Config() {
	filePath := conf.WorkingDir + "/.pull/cloud18.toml"
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		repman.Conf.ReadCloud18Config(repman.ViperConfig, filePath)
	}
}

func (repman *ReplicationManager) ComputeFileChecksum(filePath string) (hash.Hash, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	hasher := md5.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return nil, fmt.Errorf("error computing file hash: %v", err)
	}
	return hasher, nil
}

func (repman *ReplicationManager) CheckCloud18Config(filePath string) {
	// Define the file path for cloud18.toml

	currentChecksum, err := repman.ComputeFileChecksum(filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error checking file %s: %v", filePath, err)
		}
		return
	}

	// First-time initialization
	if repman.cloud18CheckSum == nil {
		repman.ReadCloud18Config()
		repman.cloud18CheckSum = currentChecksum
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Initialized cloud18.toml checksum")
	} else if !bytes.Equal(repman.cloud18CheckSum.Sum(nil), currentChecksum.Sum(nil)) {
		// File has changed, reload configuration
		repman.ReadCloud18Config()
		repman.cloud18CheckSum = currentChecksum
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "cloud18.toml has been updated")
	}
}

func (repman *ReplicationManager) LoadPeerJson() error {
	filePath := filepath.Join(repman.Conf.WorkingDir, ".pull", "peer.json")

	fstat, err := os.Stat(filePath)
	if err != nil {
		repman.PeerClusters = make([]config.PeerCluster, 0)
		if !os.IsNotExist(err) {
			repman.Logrus.Errorf("failed reading peer file: %v", err)
		}
		return err
	}

	modTime := fstat.ModTime()

	if oldModTime, ok := repman.ModTimes["peer"]; ok && oldModTime.Equal(modTime) {
		return nil // No changes in the file modification time
	}

	repman.ModTimes["peer"] = modTime

	content, err := os.ReadFile(filePath)
	if err != nil {
		repman.PeerClusters = make([]config.PeerCluster, 0)
		if !os.IsNotExist(err) {
			repman.Logrus.Errorf("failed reading peer file: %v", err)
		}
		return err
	}

	// Calculate the checksum
	newHash := md5.New()
	newHash.Write(content)

	// Compare with the existing checksum
	if oldHash, ok := repman.CheckSumConfig["peer"]; ok && bytes.Equal(oldHash.Sum(nil), newHash.Sum(nil)) {
		return nil // No changes in the file content
	}

	// Decode JSON
	var PeerList []config.PeerCluster
	if err := json.Unmarshal(content, &PeerList); err != nil {
		repman.Logrus.Errorf("failed to decode peer JSON: %v", err)
		return err
	}

	// Update state
	repman.PeerClusters = PeerList
	repman.CheckSumConfig["peer"] = newHash

	return nil

}

func (repman *ReplicationManager) LoadPartnersJson() error {
	filePath := filepath.Join(repman.Conf.WorkingDir, ".pull", "partners.json")

	fstat, err := os.Stat(filePath)
	if err != nil {
		repman.Partners = make([]config.Partner, 0)
		if !os.IsNotExist(err) {
			repman.Logrus.Errorf("failed reading partners file: %v", err)
		}

		return err
	}

	modTime := fstat.ModTime()

	if oldModTime, ok := repman.ModTimes["partners"]; ok && oldModTime.Equal(modTime) {
		return nil // No changes in the file modification time
	}

	repman.ModTimes["partners"] = modTime

	content, err := os.ReadFile(filePath)
	if err != nil {
		repman.Partners = make([]config.Partner, 0)
		if !os.IsNotExist(err) {
			repman.Logrus.Errorf("failed reading partners file: %v", err)
		}
		return err
	}

	// Calculate the checksum
	newHash := md5.New()
	newHash.Write(content)

	// Compare with the existing checksum
	if oldHash, ok := repman.CheckSumConfig["partners"]; ok && bytes.Equal(oldHash.Sum(nil), newHash.Sum(nil)) {
		return nil // No changes in the file content
	}

	// Decode JSON
	var PartnerList []config.Partner
	if err := json.Unmarshal(content, &PartnerList); err != nil {
		repman.Logrus.Errorf("failed to decode partners JSON: %v", err)
		return err
	}

	// Update state
	repman.Partners = PartnerList
	repman.CheckSumConfig["partners"] = newHash
	repman.SetCloudPartner()
	return nil

}

// Ensures ".pull/" is in .gitignore.
func (repman *ReplicationManager) AddPullToGitignore() {
	gitignoreFile := repman.Conf.WorkingDir + "/.gitignore"
	lineToAdd := ".pull/"

	// Check if .gitignore exists
	if _, err := os.Stat(gitignoreFile); os.IsNotExist(err) {
		// If .gitignore doesn't exist, create it and write the line
		err := os.WriteFile(gitignoreFile, []byte(lineToAdd+"\n"), 0644)
		if err != nil {
			fmt.Println("Error creating .gitignore:", err)
		}
		return
	}

	// Open .gitignore for reading and appending
	file, err := os.OpenFile(gitignoreFile, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		fmt.Println("Error opening .gitignore:", err)
		return
	}
	defer file.Close()

	// Check if the line already exists
	scanner := bufio.NewScanner(file)
	lineExists := false
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == lineToAdd {
			lineExists = true
			break
		}
	}

	if scanner.Err() != nil {
		fmt.Println("Error reading .gitignore:", scanner.Err())
		return
	}

	// Append the line if it doesn't already exist
	if !lineExists {
		_, err := file.WriteString(lineToAdd + "\n")
		if err != nil {
			fmt.Println("Error appending to .gitignore:", err)
		}
	}
}

// Ensures ".tmp/" is in .gitignore.
func (repman *ReplicationManager) AddTempDirToGitignore() {
	gitignoreFile := repman.Conf.WorkingDir + "/.gitignore"
	lineToAdd := ".tmp/"

	// Check if .gitignore exists
	if _, err := os.Stat(gitignoreFile); os.IsNotExist(err) {
		// If .gitignore doesn't exist, create it and write the line
		err := os.WriteFile(gitignoreFile, []byte(lineToAdd+"\n"), 0644)
		if err != nil {
			fmt.Println("Error creating .gitignore:", err)
		}
		return
	}

	// Open .gitignore for reading and appending
	file, err := os.OpenFile(gitignoreFile, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		fmt.Println("Error opening .gitignore:", err)
		return
	}
	defer file.Close()

	// Check if the line already exists
	scanner := bufio.NewScanner(file)
	lineExists := false
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == lineToAdd {
			lineExists = true
			break
		}
	}

	if scanner.Err() != nil {
		fmt.Println("Error reading .gitignore:", scanner.Err())
		return
	}

	// Append the line if it doesn't already exist
	if !lineExists {
		_, err := file.WriteString(lineToAdd + "\n")
		if err != nil {
			fmt.Println("Error appending to .gitignore:", err)
		}
	}
}

func (repman *ReplicationManager) MoveConfigsToTmpDir(path string) error {
	// Create the .tmp directory
	tmpDir := filepath.Join(path, ".tmp")
	err := os.MkdirAll(tmpDir, 0755)
	if err != nil {
		repman.Logrus.Errorf("Error creating .tmp directory: %s", err)
		return err
	}

	err = filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the .tmp directory itself
		if info.IsDir() && filePath == tmpDir {
			return filepath.SkipDir
		}

		// Process only .toml and .json files
		if !info.IsDir() && (strings.HasSuffix(info.Name(), ".toml") || strings.HasSuffix(info.Name(), ".json")) {
			// Calculate relative path and target path in .tmp
			relPath, err := filepath.Rel(path, filePath)
			if err != nil {
				repman.Logrus.Errorf("Error calculating relative path for %s: %s", filePath, err)
				return err
			}
			newPath := filepath.Join(tmpDir, relPath)

			// Create directories in .tmp as needed
			err = os.MkdirAll(filepath.Dir(newPath), 0755)
			if err != nil {
				repman.Logrus.Errorf("Error creating directories for %s: %s", newPath, err)
				return err
			}

			// Move the file
			err = os.Rename(filePath, newPath)
			if err != nil {
				repman.Logrus.Errorf("Error moving file %s to %s: %s", filePath, newPath, err)
				return err
			}
		}
		return nil
	})

	if err != nil {
		repman.Logrus.Errorf("Error moving files to .tmp directory: %s", err)
		return err
	}

	return nil
}

func (repman *ReplicationManager) RestoreConfigsFromTmpDir(path string) error {
	// Define the .tmp directory
	tmpDir := filepath.Join(path, ".tmp")

	// Check if the .tmp directory exists
	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		log.Printf(".tmp directory does not exist in %s\n", path)
		return nil
	}

	err := filepath.Walk(tmpDir, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Only process files
		if !info.IsDir() {
			// Calculate the original path of the file
			relPath, err := filepath.Rel(tmpDir, filePath)
			if err != nil {
				log.Printf("Error calculating relative path for %s: %s\n", filePath, err)
				return err
			}

			originalPath := filepath.Join(path, relPath)

			// Ensure the parent directory exists
			err = os.MkdirAll(filepath.Dir(originalPath), 0755)
			if err != nil {
				log.Printf("Error creating directories for %s: %s\n", originalPath, err)
				return err
			}

			// Move the file back to its original location
			err = os.Rename(filePath, originalPath)
			if err != nil {
				log.Printf("Error moving file %s back to %s: %s\n", filePath, originalPath, err)
				return err
			}
		}

		return nil
	})

	if err != nil {
		log.Printf("Error restoring files from .tmp directory: %s\n", err)
		return err
	}

	// Remove the .tmp directory after restoration
	err = os.RemoveAll(tmpDir)
	if err != nil {
		log.Printf("Error removing .tmp directory: %s\n", err)
		return err
	}

	log.Println("Restoration completed successfully.")
	return nil
}

func (repman *ReplicationManager) PushConfigToGit() error {
	url := repman.Conf.GitUrl
	tok := repman.Conf.GetDecryptedValue("git-acces-token")
	user := repman.Conf.GitUsername
	path := repman.Conf.WorkingDir
	clusterList := repman.ClusterList

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg, "Push to git : tok %s, dir %s, user %s, clustersList : %v\n", repman.Conf.PrintSecret(tok), path, user, clusterList)

	auth := &git_https.BasicAuth{
		Username: user, // yes, this can be anything except an empty string
		Password: tok,
	}

	var r *git.Repository
	if _, err := os.Stat(path + "/.git"); os.IsNotExist(err) {
		if !repman.Conf.ConfRestoreOnStart {
			repman.MoveConfigsToTmpDir(path)
		}
		// Clone the given repository to the given directory
		r, err = git.PlainClone(path, false, &git.CloneOptions{
			URL:               url,
			RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
			Auth:              auth,
		})
		if err != nil {
			if err == transport.ErrRepositoryNotFound {
				repman.Conf.CreateGitlabProjects()
				r, err = git.PlainClone(path, false, &git.CloneOptions{
					URL:               url,
					RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
					Auth:              auth,
				})
				repman.RestoreConfigsFromTmpDir(path)
				if err != nil {
					repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg, "Git error : cannot Clone %s repository : %s", url, err)
					return err
				}
			} else {
				repman.RestoreConfigsFromTmpDir(path)
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg, "Git error : cannot Clone %s repository : %s", url, err)
				return err
			}
		}

		w, err := r.Worktree()
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Git error : cannot Worktree : %s", err)
			return err
		}

		// checkout and keep files
		w.Checkout(&git.CheckoutOptions{Keep: true})

		if !repman.Conf.ConfRestoreOnStart {
			repman.RestoreConfigsFromTmpDir(path)
		}
	} else {
		r, err = git.PlainOpen(path)
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Git error : cannot PlainOpen : %s", err)
			return err
		}
	}

	w, err := r.Worktree()
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Git error : cannot Worktree : %s", err)
		return err
	}

	if len(clusterList) != 0 {
		for _, name := range clusterList {
			// Adds the new file to the staging area.
			err = w.AddGlob(name + "/*.toml")
			if err != nil {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Git error : cannot Add %s : %s", name+"/*.toml", err)
			}

			if _, err := os.Stat(repman.Conf.WorkingDir + "/" + name + "/agents.json"); !os.IsNotExist(err) {
				_, err = w.Add(name + "/agents.json")
				if err != nil {
					repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Git error : cannot Add %s : %s", name+"/agents.json", err)
				}
				_, err = w.Add(name + "/queryrules.json")
				if err != nil {
					repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Git error : cannot Add %s : %s", name+"/queryrules.json", err)
				}
			}
		}
	}

	if _, err := os.Stat(repman.Conf.WorkingDir + "/default.toml"); !os.IsNotExist(err) {
		_, err = w.Add("default.toml")
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Git error : cannot Add default.toml : %s", err)
		}
	}

	msg := "Update file"

	_, err = w.Commit(msg, &git.CommitOptions{
		Author: &git_obj.Signature{
			Name: "Replication-manager",
			When: time.Now(),
		},
		All: true,
	})

	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Git error : cannot Commit : %s", err)
		return err
	}

	// push using default options
	err = r.Push(&git.PushOptions{Auth: auth, RemoteURL: url})
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Git error : cannot Push : %s", err)
	}

	return err
}
