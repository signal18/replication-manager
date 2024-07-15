package config

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"

	"github.com/signal18/replication-manager/share"
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

type MDevIssueList map[string]MDevIssue

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
			vers = append(vers, line[i])
		}
	}
	issue.Versions = vers

	fixs := make([]string, 0)
	for i := idx.FixVersions.Min; i <= idx.FixVersions.Max; i++ {
		if len(line[i]) > 0 {
			fixs = append(fixs, line[i])
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

type MDevIssueMap struct {
	*sync.Map
}

func NewMDevIssueMap() *MDevIssueMap {
	s := new(sync.Map)
	m := &MDevIssueMap{Map: s}
	return m
}

func (m *MDevIssueMap) Get(key string) *MDevIssue {
	if v, ok := m.Load(key); ok {
		return v.(*MDevIssue)
	}
	return nil
}

func (m *MDevIssueMap) CheckAndGet(key string) (*MDevIssue, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*MDevIssue), true
	}
	return nil, false
}

func (m *MDevIssueMap) Set(key string, value *MDevIssue) {
	m.Store(key, value)
}

func (m *MDevIssueMap) ToNormalMap(c map[string]*MDevIssue) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the MDevIssueMap to the output map
	m.Callback(func(key string, value *MDevIssue) bool {
		c[key] = value
		return true
	})
}

func (m *MDevIssueMap) ToNewMap() map[string]*MDevIssue {
	result := make(map[string]*MDevIssue)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*MDevIssue)
		return true
	})
	return result
}

func (m *MDevIssueMap) Callback(f func(key string, value *MDevIssue) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*MDevIssue))
	})
}

func (m *MDevIssueMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalMDevIssueMap(m *MDevIssueMap, c map[string]*MDevIssue) *MDevIssueMap {
	if m == nil {
		m = NewMDevIssueMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromMDevIssueMap(m *MDevIssueMap, c *MDevIssueMap) *MDevIssueMap {
	if m == nil {
		m = NewMDevIssueMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value *MDevIssue) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}

func (m *MDevIssueMap) MDevParseCSV(filename string, replace bool) error {
	log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Infof("[MDEV-Parser] Opening csv in shared repo dir : %s", filename)

	file, err := os.Open(filename)
	if err != nil {
		log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Errorf("[MDEV-Parser] failed to open csv in shared repo dir : %s", err.Error())
		return err
	}
	defer file.Close()

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
		ln, _ := csvr.FieldPos(0)

		if header {
			//Parse Header
			idx.parseHeader(line)
			log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Infof("[MDEV-Parser] Indexes : %v", idx)
			header = false
		} else {
			//Parse Content
			issue := new(MDevIssue)
			if err = issue.parseContent(line, idx); err == nil {
				if replace {
					m.Store(issue.Key, issue)
				} else {
					m.LoadOrStore(issue.Key, issue)
				}
				if log.GetLevel() == log.DebugLevel {
					jsline, _ := json.MarshalIndent(issue, "", "\t")
					log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Debugf("[MDEV-Parser] Line:%d source:(%v)", ln, line)
					log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Debugf("[MDEV-Parser] Line:%d result:(%s)", ln, jsline)
				}
			} else {

				log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Errorf("[MDEV-Parser] Skip line number: %d", ln)
			}
		}
	}

	return nil
}

func (m *MDevIssueMap) MDevWriteJSONFile(filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Errorf("[MDEV-Parser] failed to csv in shared repo dir : %s", err.Error())
		return err
	}
	defer file.Close()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "\t")
	err = enc.Encode(m.ToNewMap())
	if err != nil {
		log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Errorf("[MDEV-Parser] failed to csv in shared repo dir : %s", err.Error())
		return err
	}

	return nil
}

func (m *MDevIssueMap) MDevLoadJSONFile(filename string) error {
	var err error
	var content []byte = make([]byte, 0)

	log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Infof("[MDEV-Parser] Loading JSON MDev file at %s", filename)
	content, err = os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Errorf("[MDEV-Parser] No JSON file. Initiate empty JSON file: %s", filename)
			var file *os.File
			file, err = os.Create(filename)
			file.Close()
		}
		if err != nil {
			log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Errorf("[MDEV-Parser] failed to open json : %s", err.Error())
			return err
		}
	}
	tmp := make(map[string]MDevIssue)
	if len(content) > 0 {
		err = json.Unmarshal(content, &tmp)
		if err != nil {
			log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Errorf("[MDEV-Parser] failed to parse JSON : %s", err.Error())
			return err
		}

		for k, v := range tmp {
			m.Store(k, &v)
		}
	} else {
		log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Warn("[MDEV-Parser] Skip parsing empty JSON file")
	}

	return nil
}

func (conf *Config) UpdateMDevJSONFile(csvfile string, replace bool, verbose bool) error {
	var mdev *MDevIssueMap = NewMDevIssueMap()
	var err error
	var jsonfile string = conf.WorkingDir + "/mdev.json"

	if verbose {
		log.Info("Log Verbose")
		log.SetLevel(log.DebugLevel)
	}

	conf.InitMDevJSONFile(jsonfile)
	//Populate existing list from JSON
	err = mdev.MDevLoadJSONFile(jsonfile)
	if err != nil {
		return err
	}
	//Populate existing list from JSON
	err = mdev.MDevParseCSV(csvfile, replace)
	if err != nil {
		return err
	}
	//Write back to JSON File
	err = mdev.MDevWriteJSONFile(jsonfile)
	if err != nil {
		return err
	}
	return nil
}

func (conf *Config) InitMDevJSONFile(filename string) error {
	var err error
	var content []byte = make([]byte, 0)

	// Init if not exists
	if _, err := os.Stat(filename); err != nil {
		if os.IsNotExist(err) {
			if conf.WithEmbed == "ON" {
				content, err = share.EmbededDbModuleFS.ReadFile("repo/mdev.json")
			} else {
				content, err = os.ReadFile(conf.ShareDir + "/repo/mdev.json")
			}
			if err != nil {
				log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Errorf("[MDEV-Parser] failed to read JSON from shared dir: %s", err.Error())
				log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Infof("[MDEV-Parser] Init empty json file at: %s", filename)
			}

			err = os.WriteFile(filename, content, 0644)
			if err != nil {
				log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Errorf("[MDEV-Parser] failed to write JSON file: %s", err.Error())
			}
		} else {
			log.WithFields(log.Fields{"cluster": "none", "module": "mdev"}).Errorf("[MDEV-Parser] failed to read JSON file: %s", err.Error())
		}
	}
	return err
}
