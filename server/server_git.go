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
	"github.com/signal18/replication-manager/peer"
	"github.com/signal18/replication-manager/utils/githelper"
	"github.com/signal18/replication-manager/utils/meethelper"
	log "github.com/sirupsen/logrus"
)

func (repman *ReplicationManager) GetIsGitPush() bool {
	repman.GitPushLock.Lock()
	defer repman.GitPushLock.Unlock()
	return repman.IsGitPush
}

func (repman *ReplicationManager) SetIsGitPush(val bool) {
	repman.GitPushLock.Lock()
	defer repman.GitPushLock.Unlock()
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
	if repman.GetIsGitPush() {
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
		gituser := conf.Cloud18GitUser
		gitpassword := conf.GetDecryptedValue("cloud18-gitlab-password")
		acces_tok, err := githelper.GetGitLabTokenBasicAuth(gituser, gitpassword, conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))
		if err != nil {
			if conf.Verbose || conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlErr) {
				repman.Logrus.Errorf("%s\n", err.Error()+conf.GetDecryptedValue("cloud18-gitlab-password"))
			}
			conf.Cloud18 = false
			return err
		}

		//to get meet token and create a client while login
		userID, err := meethelper.CreateMeetUserClient(gituser, gitpassword, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
		if err != nil {
			if repman.Conf.LogSupport {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "Error retrieving meet token: %s", err)
			}
		} else {
			repman.MeetUserID = userID
			for _, cluster := range repman.Clusters {
				cluster.MeetUserID = userID
			}
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "Meet token is retrieved")
		}

		if conf.Cloud18Domain == "" {
			return fmt.Errorf("Cloud18Domain is empty")
		}

		if conf.Cloud18SubDomain == "" {
			return fmt.Errorf("Cloud18SubDomain is empty")
		}

		if conf.Cloud18SubDomainZone == "" {
			return fmt.Errorf("Cloud18SubDomainZone is empty")
		}

		repman.SetCloudPartner()

		uid, err := githelper.GetGitLabUserId(acces_tok, conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))
		if err != nil {
			if conf.Verbose || conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlErr) {
				repman.Logrus.Errorf("%s\n", err.Error())
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
				repman.Logrus.Errorf("%s\n", err.Error())
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
			repman.Logrus.WithField("group", repman.ClusterList[cfgGroupIndex]).Infof("%s", err.Error())
			return err
		}

	}

	return nil
}

func (repman *ReplicationManager) PushAllConfigsToGit() error {
	defer func() {
		if r := recover(); r != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Error pushing to git: %v", r)
		}
	}()

	if repman.Conf.GitUrl == "" {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlInfo, "No Git URL provided, skipping push")
		return nil
	}

	repman.IsGitPush = true
	defer func() {
		repman.IsGitPush = false
	}()

	repman.AddPullToGitignore()
	repman.AddTempDirToGitignore()

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlInfo, "Pushing All Configs To Git")

	err := repman.PushConfigToGit()
	if err != nil && err == transport.ErrRepositoryNotFound {
		os.RemoveAll(repman.Conf.WorkingDir + "/.git")
		err := repman.PushConfigToGit()
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Error pushing to git: %v", err)
			return err
		}
	}

	// Count the commits
	commits, err := repman.CountAllCommits()
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlWarn, "Error counting commits: %v", err)
		return err
	}

	if commits >= 10 {
		os.RemoveAll(repman.Conf.WorkingDir + "/.git")
		err := repman.ShallowClone()
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Error shallow cloning: %v", err)
			return err
		}
	}

	return nil
}

func (repman *ReplicationManager) PullCloud18Configs() {
	gm := repman.ConfigManager.GetGitManager()
	if gm == nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Git manager not initialized")
		return
	}
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Queue Pulling Cloud18 Configs")
	repman.ConfigManager.GitPullDir() // Queue the pull
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Waiting for Pulling Cloud18 Configs")

	// Wait for start pull signal
	<-gm.PullCh
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Pulling Cloud18 Configs")

	defer func() {
		// Inform the GitManager that the pull is done
		gm.DonePullCh <- struct{}{}
	}()

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
			repman.UpdateLocalPeer()
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
						repman.Logrus.Errorf("Config error in %s: %s", repman.Conf.WorkingDir+"/"+f.Name()+"/"+f.Name()+".toml", err.Error())
					}
					repman.Confs[f.Name()] = repman.GetClusterConfig(repman.ViperConfig, repman.Conf.ImmuableFlagMap, repman.Conf.DynamicFlagMap, f.Name(), *repman.Conf)
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
		if !os.IsNotExist(err) {
			repman.Logrus.Errorf("failed reading peer file: %v", err)
		}
		return err
	}

	modTime := fstat.ModTime()

	if oldModTime, ok := repman.ModTimes["peer"]; ok && oldModTime.Equal(modTime) {
		go repman.PeerManager.GetAllHealthStatus()
		return nil // No changes in the file modification time
	}

	repman.ModTimes["peer"] = modTime

	content, err := os.ReadFile(filePath)
	if err != nil {
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
		go repman.PeerManager.GetAllHealthStatus()
		return nil // No changes in the file content
	}

	// Decode JSON
	var PeerList []*peer.PeerCluster
	if err := json.Unmarshal(content, &PeerList); err != nil {
		repman.Logrus.Errorf("failed to decode peer JSON: %v", err)
		return err
	}

	if len(PeerList) > 0 {
		repman.PeerManager.BatchUpdateClusters(PeerList, true)
	}

	// Update state
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

	// Log basic information
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg,
		"Push to git: user=%s, dir=%s, clusters=%v", user, path, clusterList)

	auth := &git_https.BasicAuth{
		Username: user, // Can be any non-empty string
		Password: tok,
	}

	var r *git.Repository
	start := time.Now()

	// Check if .git directory exists
	if _, err := os.Stat(filepath.Join(path, ".git")); os.IsNotExist(err) {
		cloneopt := &git.CloneOptions{
			URL:               url,
			RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
			Auth:              auth,
			Depth:             1, // Shallow clone
		}

		if !repman.Conf.ConfRestoreOnStart {
			cloneopt.NoCheckout = true
		}

		// Perform shallow clone for better performance
		r, err = git.PlainClone(path, false, cloneopt)

		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg,
			"Clone took: %s", time.Since(start))

		// Handle repository not found
		if err != nil {
			if err == transport.ErrRepositoryNotFound {
				repman.Conf.CreateGitlabProjects()
				r, err = git.PlainClone(path, false, &git.CloneOptions{
					URL:               url,
					RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
					Auth:              auth,
					Depth:             1,
				})
			}
			if err != nil {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
					"Git error: cannot clone %s: %s", url, err)
				return err
			}
		}
	} else {
		// Open existing repository
		r, err = git.PlainOpen(path)
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
				"Git error: cannot open repo: %s", err)
			return err
		}
	}

	// Open the worktree
	w, err := r.Worktree()
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
			"Git error: cannot get worktree: %s", err)
		return err
	}

	var changedFiles []string

	// Add specific files without using AddGlob
	for _, name := range clusterList {
		dirPath := filepath.Join(path, name)
		files, err := os.ReadDir(dirPath)
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
				"Error reading directory %s: %s", dirPath, err)
			continue
		}

		// Add .toml files
		for _, file := range files {
			if filepath.Ext(file.Name()) == ".toml" {
				filePath := filepath.Join(name, file.Name())
				if _, err := w.Add(filePath); err == nil {
					changedFiles = append(changedFiles, filePath)
				} else {
					repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
						"Git error: cannot add %s: %s", filePath, err)
				}
			}
		}

		// Add agents.json and queryrules.json if they exist
		for _, jsonFile := range []string{"agents.json", "queryrules.json"} {
			jsonPath := filepath.Join(name, jsonFile)
			if _, err := os.Stat(filepath.Join(path, jsonPath)); !os.IsNotExist(err) {
				if _, err := w.Add(jsonPath); err == nil {
					changedFiles = append(changedFiles, jsonPath)
				} else {
					repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
						"Git error: cannot add %s: %s", jsonPath, err)
				}
			}
		}
	}

	// Add default.toml if it exists
	defaultToml := "default.toml"
	if _, err := os.Stat(filepath.Join(path, defaultToml)); !os.IsNotExist(err) {
		if _, err := w.Add(defaultToml); err == nil {
			changedFiles = append(changedFiles, defaultToml)
		} else {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
				"Git error: cannot add %s: %s", defaultToml, err)
		}
	}

	// Skip commit if no files were changed
	if len(changedFiles) == 0 {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg,
			"No changes detected, skipping commit.")
		return nil
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg,
		"Files changed: %v", changedFiles)

	// Commit the changes
	commitStart := time.Now()
	_, err = w.Commit("Update configuration", &git.CommitOptions{
		Author: &git_obj.Signature{
			Name: "Replication Manager",
			When: time.Now(),
		},
	})
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg,
		"Commit took: %s", time.Since(commitStart))

	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
			"Git error: cannot commit: %s", err)
		return err
	}

	// Push changes
	pushStart := time.Now()
	err = r.Push(&git.PushOptions{Auth: auth})
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg,
		"Push took: %s", time.Since(pushStart))

	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
			"Git error: cannot push: %s", err)
	}

	return err
}

func (repman *ReplicationManager) CountAllCommits() (int, error) {
	mainPath := repman.Conf.WorkingDir

	// Open the repository
	r, err := git.PlainOpen(mainPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open repository at %s: %w", mainPath, err)
	}

	r.Fetch(&git.FetchOptions{Force: true, Auth: &git_https.BasicAuth{Username: repman.Conf.GitUsername, Password: repman.Conf.GetDecryptedValue("git-acces-token")}})

	commitIter, err := r.Log(&git.LogOptions{All: true})
	if err != nil {
		return 0, fmt.Errorf("failed to get commit iterator: %w", err)
	}

	commitCount := 0
	// Count commits for this branch/tag
	err = commitIter.ForEach(func(c *git_obj.Commit) error {
		commitCount++
		return nil
	})

	return commitCount, nil
}

func (repman *ReplicationManager) ShallowClone() error {
	url := repman.Conf.GitUrl
	tok := repman.Conf.GetDecryptedValue("git-acces-token")
	user := repman.Conf.GitUsername
	path := repman.Conf.WorkingDir

	auth := &git_https.BasicAuth{
		Username: user, // Can be any non-empty string
		Password: tok,
	}

	// Perform shallow clone for better performance
	_, err := git.PlainClone(path, false, &git.CloneOptions{
		URL:               url,
		RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
		Auth:              auth,
		Depth:             1, // Shallow clone
		NoCheckout:        true,
	})

	return err
}
