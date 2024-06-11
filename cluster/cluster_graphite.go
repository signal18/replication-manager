package cluster

import (
	"bufio"
	"errors"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/signal18/replication-manager/config"
)

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

	cluster.ClusterGraphite.PopulateWhitelistRegexp()
	cluster.ClusterGraphite.PopulateBlacklistRegexp()

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

func (cg *ClusterGraphite) PopulateWhitelistRegexp() error {
	conf := cg.cl.Conf
	if conf.GraphiteWhitelist {

		fname := conf.WorkingDir + "/" + cg.cl.Name + "/whitelist.conf"
		template := conf.ShareDir + "/whitelist.conf.template"
		file, err := os.Open(fname)
		if err != nil {
			//Create file if not exists
			if errors.Is(err, fs.ErrNotExist) {
				err = cg.CopyFromShareDir("whitelist", template, fname)
				//Create return if error
				if err != nil {
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

func (cg *ClusterGraphite) PopulateBlacklistRegexp() error {
	conf := cg.cl.Conf
	if conf.GraphiteBlacklist {
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

				file, err = os.Open(fname)

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
