// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package misc

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/siddontang/go/log"
)

func ConvertCSVtoJSON(sourcefile string, destfile string, separator string) error {
	file, err := os.Open(sourcefile)
	if err != nil {
		log.Errorf("failed opening file because: %s", err.Error())
		return err
	}
	defer file.Close()

	r := csv.NewReader(file)
	r.TrimLeadingSpace = false
	r.Comma = []rune(separator)[0]
	rows, err := r.ReadAll()
	if err != nil {
		log.Fatal(err)
	}
	var res interface{}
	if len(rows) > 1 {
		header := rows[0]
		rows = rows[1:]
		objs := make([]map[string]string, len(rows))
		for y, row := range rows {
			obj := map[string]string{}
			for x, cell := range row {
				obj[header[x]] = cell
			}
			objs[y] = obj
		}
		res = objs
	} else {
		res = []map[string]string{}
	}
	output, err := json.Marshal(res)
	if err != nil {
		log.Fatal(err)
	}
	fileout, err := os.OpenFile(destfile, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err

	}
	defer fileout.Close()
	fileout.Truncate(0)
	fileout.Write(output)
	fileout.Write([]byte("\n"))

	return nil
}

// parses the raw stats CSV output to a json string
func CsvToJson(csvInput string) (string, error) {

	csvReader := csv.NewReader(strings.NewReader(csvInput))
	lineCount := 0
	var headers []string
	var result bytes.Buffer
	var item bytes.Buffer
	result.WriteString("[")

	for {
		// read just one record, but we could ReadAll() as well
		record, err := csvReader.Read()

		if err == io.EOF {

			// ugly fix for when there are no records to read so we need to close
			// the json array directly.
			if len(result.String()) > 1 {
				result.Truncate(int(len(result.String()) - 1))
			}

			result.WriteString("]")
			break
		} else if err != nil {
			fmt.Println("Error:", err)
			return "", err
		}

		if lineCount == 0 {
			headers = record[:]
			lineCount += 1
		} else {
			item.WriteString("{")
			for i := 0; i < len(headers); i++ {
				item.WriteString("\"" + headers[i] + "\": \"" + record[i] + "\"")
				if i == (len(headers) - 1) {
					item.WriteString("}")
				} else {
					item.WriteString(",")
				}
			}
			result.WriteString(item.String() + ",")
			item.Reset()
			lineCount += 1
		}
	}
	return result.String(), nil
}
