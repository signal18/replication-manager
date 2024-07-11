package config

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
)

var JiraURL string = "https://jira.mariadb.org/browse/"

const labelKey string = "Issue key"
const labelStatus string = "Status"
const labelUpdated string = "Updated"
const labelComp string = "Component/s"
const labelVersionAffected string = "Affects Version/s"
const labelVersionFixed string = "Fix Version/s"

type MDevIssue struct {
	Key         string   `json:"key"`
	Status      string   `json:"status"`
	Updated     int64    `json:"updated"`
	Components  []string `json:"components"`
	Versions    []string `json:"versions"`
	FixVersions []string `json:"fixVersions"`
}

type MDevIssueList []*MDevIssue

type IndexRange struct {
	Min int
	Max int
}

type MDevIssueExists struct {
	Key         bool
	Status      bool
	Updated     bool
	Components  bool
	Versions    bool
	FixVersions bool
}

type MDevIssueIndex struct {
	Key         int
	Status      int
	Updated     int
	Components  IndexRange
	Versions    IndexRange
	FixVersions IndexRange
	Found       MDevIssueExists
}

func (issue *MDevIssue) getURL() string {
	return JiraURL + issue.Key
}

func (idx *MDevIssueIndex) parseHeader(line []string) {
	prev := ""
	for i, v := range line {
		switch v {
		case labelKey:
			idx.Key = i
			idx.Found.Key = true
		case labelStatus:
			idx.Status = i
			idx.Found.Status = true
		case labelUpdated:
			idx.Updated = i
			idx.Found.Updated = true
		case labelComp:
			if prev != labelComp {
				idx.Components.Min = i
				prev = labelComp
			}
			idx.Components.Max = i
			idx.Found.Components = true
		case labelVersionAffected:
			if prev != labelVersionAffected {
				idx.Versions.Min = i
				prev = labelVersionAffected
			}
			idx.Versions.Max = i
			idx.Found.Versions = true
		case labelVersionFixed:
			if prev != labelVersionFixed {
				idx.FixVersions.Min = i
				prev = labelVersionFixed
			}
			idx.FixVersions.Max = i
			idx.Found.FixVersions = true
		}
	}
}

func (issue *MDevIssue) parseContent(line []string, idx *MDevIssueIndex) error {
	issue.Key = line[idx.Key]
	issue.Status = line[idx.Status]

	comps := make([]string, 0)
	for i := idx.Components.Min; i <= idx.Components.Max; i++ {
		if len(line[i]) > 0 {
			comps = append(comps, line[i])
		}
	}
	issue.Components = comps

	vers := make([]string, 0)
	for i := idx.Versions.Min; i <= idx.Versions.Max; i++ {
		if len(line[i]) > 0 {
			comps = append(comps, line[i])
		}
	}
	issue.Versions = vers

	fixs := make([]string, 0)
	for i := idx.FixVersions.Min; i <= idx.FixVersions.Max; i++ {
		if len(line[i]) > 0 {
			comps = append(comps, line[i])
		}
	}
	issue.FixVersions = fixs

	if u, err := time.Parse("2006-01-02 15:04", line[idx.Updated]); err == nil {
		issue.Updated = u.Unix()
	} else {
		log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Errorf("[MDEV-Parser] Error parse content: %s", err.Error())
		return err
	}

	return nil
}

func (conf *Config) MDevParseCSV() (MDevIssueList, error) {
	file, err := os.Open(conf.ShareDir + "/repo/mariadb_alert.csv")
	if err != nil {
		log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Errorf("[MDEV-Parser] failed to open csv in shared repo dir : %s", err.Error())
		return nil, err
	}

	issues := make(MDevIssueList, 0)

	csvr := csv.NewReader(file)
	header := true
	idx := new(MDevIssueIndex)

	csvr.Comma = ';'

	for {
		line, err := csvr.Read()
		if err != nil {
			if err != io.EOF {
				log.Error(err)
			}
			break
		}

		if header {
			//Parse Header
			idx.parseHeader(line)
			header = false
		} else {
			//Parse Content
			issue := new(MDevIssue)
			if err = issue.parseContent(line, idx); err == nil {
				issues = append(issues, issue)
			} else {
				ln, _ := csvr.FieldPos(0)
				log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Errorf("[MDEV-Parser] Skip line number: %d", ln)
			}
		}
	}

	return issues, nil
}

// We should change to Premium users location later
func (conf *Config) MDevWriteJSONFile(list MDevIssueList) error {
	file, err := os.Create(conf.ShareDir + "/repo/mdev.json")
	if err != nil {
		log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Errorf("[MDEV-Parser] failed to csv in shared repo dir : %s", err.Error())
		return err
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "\t")
	err = enc.Encode(list)
	if err != nil {
		log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Errorf("[MDEV-Parser] failed to csv in shared repo dir : %s", err.Error())
		return err
	}

	return nil
}
