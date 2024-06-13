package cluster

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/signal18/replication-manager/config"
)

type GraphiteFilterList struct {
	Whitelist string `json:"whitelist"`
	Blacklist string `json:"blacklist"`
}

type ClusterGraphite struct {
	cl           *Cluster `json:"-"`
	UseWhitelist bool
	UseBlacklist bool
	Whitelist    []*regexp.Regexp
	Blacklist    []*regexp.Regexp
}

func (cluster *Cluster) NewClusterGraphite() {
	cluster.ClusterGraphite = &ClusterGraphite{
		cl:           cluster,
		UseWhitelist: cluster.Conf.GraphiteWhitelist,
		UseBlacklist: cluster.Conf.GraphiteBlacklist,
	}

	cluster.ReloadGraphiteFilterList()
}

func (cluster *Cluster) GetGraphiteWhitelist() []string {
	ls := make([]string, 0)
	for _, r := range cluster.ClusterGraphite.Whitelist {
		ls = append(ls, r.String())
	}

	return ls
}

func (cluster *Cluster) GetGraphiteBlacklist() []string {
	ls := make([]string, 0)
	for _, r := range cluster.ClusterGraphite.Blacklist {
		ls = append(ls, r.String())
	}

	return ls
}

func (cluster *Cluster) GetGraphiteFilterList() GraphiteFilterList {
	return GraphiteFilterList{
		Whitelist: strings.Join(cluster.GetGraphiteWhitelist(), "\n"),
		Blacklist: strings.Join(cluster.GetGraphiteBlacklist(), "\n"),
	}
}

// Wrapper for clustergraphite write file
func (cluster *Cluster) SetGraphiteFilterList(ft string, fl GraphiteFilterList) error {
	return cluster.ClusterGraphite.WriteFromFilterList(ft, fl)
}

// Wrapper for clustergraphite write file
func (cluster *Cluster) ReloadGraphiteFilterList() error {
	err := cluster.ClusterGraphite.PopulateWhitelistRegexp(false)
	if err != nil {
		return err
	}
	err = cluster.ClusterGraphite.PopulateBlacklistRegexp(false)
	if err != nil {
		return err
	}

	return nil
}

func (cg *ClusterGraphite) SetWhitelist(val bool) {
	cg.UseWhitelist = val
	cg.cl.Conf.GraphiteWhitelist = val
}

func (cg *ClusterGraphite) SetBlacklist(val bool) {
	cg.UseBlacklist = val
	cg.cl.Conf.GraphiteBlacklist = val
}

func (cg *ClusterGraphite) WriteFromFilterList(srcfunc string, fl GraphiteFilterList) error {
	conf := cg.cl.Conf
	var err error
	var ls string
	var fname string
	switch srcfunc {
	case "whitelist":
		ls = fl.Whitelist
		fname = conf.WorkingDir + "/" + cg.cl.Name + "/whitelist.conf"
	case "blacklist":
		ls = fl.Blacklist
		fname = conf.WorkingDir + "/" + cg.cl.Name + "/blacklist.conf"
	}

	cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlInfo, "[%s] reading old file : %s", srcfunc, fname)
	var wg sync.WaitGroup
	wg.Add(1)
	var backup = func(wg *sync.WaitGroup) error {
		defer wg.Done()
		content, err := os.ReadFile(fname)
		if err != nil {
			cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[%s] failed reading old file content: %s", srcfunc, err.Error())
			return err
		}

		//Backup old list
		cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlInfo, "[%s] writing to .old file : %s.old", srcfunc, fname)
		err = os.WriteFile(fname+".old", content, 0644)
		if err != nil {
			cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[%s] failed writing new file content : %s", srcfunc, err.Error())
			return err
		}

		return nil
	}

	if err = backup(&wg); err != nil {
		return err
	}

	wg.Wait()
	//This will truncate the old file
	cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlInfo, "[%s] writing new content to .conf file : %s", srcfunc, fname)
	err = os.WriteFile(fname, []byte(ls), 0644)
	if err != nil {
		cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[%s] failed writing new file content : %s", srcfunc, err.Error())
		return err
	}

	return nil
}

func (cg *ClusterGraphite) CopyFromShareDir(srcfunc string, src string, dest string) error {
	conf := cg.cl.Conf

	srcFile, err := os.Open(src)
	if err != nil {
		cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[%s] failed to read file in share dir : %s", srcfunc, err.Error())
		return err
	}
	defer srcFile.Close()

	destFile, err := os.Create(dest)
	if err != nil {
		cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[%s] failed to read file in working dir : %s", srcfunc, err.Error())
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, srcFile) // check first var for number of bytes copied
	if err != nil {
		cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[%s] failed writing file content to working dir : %s", srcfunc, err.Error())
		return err
	}

	err = destFile.Sync()
	if err != nil {
		cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[%s] failed to flush file in working dir : %s", srcfunc, err.Error())
		return err
	}

	return nil
}

func (cg *ClusterGraphite) ResetFilterListRegexp() error {
	var err error
	conf := cg.cl.Conf

	switch conf.GraphiteWhitelistTemplate {
	case config.ConstGraphiteTemplateNone:
		fname := conf.WorkingDir + "/" + cg.cl.Name + "/whitelist.conf"
		err = os.WriteFile(fname, []byte(""), 0644)
		if err != nil {
			cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Whitelist] failed to write file in working dir : %s", err.Error())
			return err
		}

		fname = conf.WorkingDir + "/" + cg.cl.Name + "/blacklist.conf"
		err = os.WriteFile(fname, []byte("*"), 0644)
		if err != nil {
			cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Blacklist] failed to write file in working dir : %s", err.Error())
			return err
		}

		cg.SetWhitelist(false)
		cg.SetBlacklist(true)

	case config.ConstGraphiteTemplateMinimal, config.ConstGraphiteTemplateGrafana:
		fname := conf.WorkingDir + "/" + cg.cl.Name + "/whitelist.conf"
		template := fmt.Sprintf("%s/whitelist.conf.%s", conf.ShareDir, conf.GraphiteWhitelistTemplate)

		err = cg.CopyFromShareDir("whitelist", template, fname)
		if err != nil {
			return err
		}

		fname = conf.WorkingDir + "/" + cg.cl.Name + "/blacklist.conf"
		err = os.WriteFile(fname, []byte(""), 0644)
		if err != nil {
			cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Blacklist] failed to write file in working dir : %s", err.Error())
			return err
		}
		cg.SetWhitelist(true)
		cg.SetBlacklist(false)
	case config.ConstGraphiteTemplateAll:
		fname := conf.WorkingDir + "/" + cg.cl.Name + "/whitelist.conf"
		err = os.WriteFile(fname, []byte("*"), 0644)
		if err != nil {
			cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Whitelist] failed to write file in working dir : %s", err.Error())
			return err
		}

		fname = conf.WorkingDir + "/" + cg.cl.Name + "/blacklist.conf"
		err = os.WriteFile(fname, []byte(""), 0644)
		if err != nil {
			cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Blacklist] failed to write file in working dir : %s", err.Error())
			return err
		}
		cg.SetWhitelist(true)
		cg.SetBlacklist(false)
	}

	cg.PopulateWhitelistRegexp(true)
	cg.PopulateBlacklistRegexp(true)
	return nil
}

func (cg *ClusterGraphite) ResetBlacklistRegexp() error {
	var err error
	conf := cg.cl.Conf

	fname := conf.WorkingDir + "/" + cg.cl.Name + "/blacklist.conf"
	template := fmt.Sprintf("%s/blacklist.conf.%s", conf.ShareDir, conf.GraphiteWhitelistTemplate)

	err = cg.CopyFromShareDir("blacklist", template, fname)
	if err != nil {
		return err
	}
	return nil
}

func (cg *ClusterGraphite) PopulateWhitelistRegexp(force bool) error {
	conf := cg.cl.Conf
	if conf.GraphiteWhitelist || force {
		fname := conf.WorkingDir + "/" + cg.cl.Name + "/whitelist.conf"
		template := fmt.Sprintf("%s/whitelist.conf.%s", conf.ShareDir, conf.GraphiteWhitelistTemplate)
		file, err := os.Open(fname)
		if err != nil {
			//Create file if not exists
			if errors.Is(err, fs.ErrNotExist) {
				err = cg.CopyFromShareDir("whitelist", template, fname)
				//Create return if error
				if err != nil {
					return err
				}
				if file, err = os.Open(fname); err != nil {
					return err
				}
			} else {
				return err
			}
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		ln := 1
		for scanner.Scan() {
			val := scanner.Text()
			if !strings.HasPrefix(val, "#") {
				regex, regErr := regexp.Compile(val)
				if regErr != nil {
					cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Whitelist] failed to parse regexp pattern on line %d : %s. Skipping", ln, val)
				} else {
					cg.Whitelist = append(cg.Whitelist, regex)
				}
			}
			ln++
		}

		if err := scanner.Err(); err != nil {
			return err
		}
	}

	return nil
}

func (cg *ClusterGraphite) PopulateBlacklistRegexp(force bool) error {
	conf := cg.cl.Conf
	if conf.GraphiteBlacklist || force {
		fname := conf.WorkingDir + "/" + cg.cl.Name + "/blacklist.conf"
		template := conf.ShareDir + "/blacklist.conf.template"
		file, err := os.Open(fname)
		if err != nil {
			//Copy file if not exists
			if errors.Is(err, fs.ErrNotExist) {
				err = cg.CopyFromShareDir("blacklist", template, fname)
				// Create return if error
				if err != nil {
					return err
				}
				if file, err = os.Open(fname); err != nil {
					return err
				}
			} else {
				return err
			}
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		ln := 1
		for scanner.Scan() {
			val := scanner.Text()
			if !strings.HasPrefix(val, "#") {
				regex, regErr := regexp.Compile(val)
				if regErr != nil {
					cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Blacklist] failed to parse regexp pattern on line %d : %s. Skipping", ln, val)
				} else {
					cg.Blacklist = append(cg.Blacklist, regex)
				}
			}
			ln++
		}

		if err := scanner.Err(); err != nil {
			return err
		}
	}

	return nil
}

func (cg *ClusterGraphite) MatchWhitelist(m string) bool {
	if len(cg.Whitelist) > 0 {
		for _, v := range cg.Whitelist {
			if v.MatchString(m) {
				return true
			}
		}

	}
	return false
}

func (cg *ClusterGraphite) MatchBlacklist(m string) bool {
	if len(cg.Blacklist) > 0 {
		for _, v := range cg.Blacklist {
			if v.MatchString(m) {
				return true
			}
		}

	}
	return false
}

func (cg *ClusterGraphite) MatchList(m string) bool {
	conf := cg.cl.Conf
	// If listed in blacklist
	if cg.UseBlacklist && cg.MatchBlacklist(m) {
		cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlDbg, "Skip metric in blacklist: %s", m)
		return false
	}

	// If listed in whitelist
	if cg.UseWhitelist {
		if cg.MatchWhitelist(m) {
			return true
		} else {
			// Not found in whitelist
			return false
		}
	}

	//Default is to store
	return true
}
