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
	"io/fs"
	"os"
	"strings"
)

func ConvertCSVtoJSON(sourcefile string, destfile string, separator string) ([]string, error) {
	var err error
	var file fs.File
	warnings := []string{}

	file, err = os.Open(sourcefile)
	if err != nil {
		return nil, fmt.Errorf("open csv %s: %w", sourcefile, err)
	}
	defer file.Close()

	r := csv.NewReader(file)
	r.TrimLeadingSpace = false
	r.Comma = []rune(separator)[0]
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read csv %s: %w", sourcefile, err)
	}
	var res interface{}
	if len(rows) > 1 {
		header := rows[0]
		rows = rows[1:]
		var objs []map[string]string

		idIndex := -1
		planIndex := -1
		for i, name := range header {
			switch strings.TrimSpace(name) {
			case "id":
				idIndex = i
			case "plan":
				planIndex = i
			}
		}

		for y, row := range rows {
			rowLine := y + 2
			if isEmptyCSVRow(row) {
				warnings = append(warnings, fmt.Sprintf("Skipping empty service plan CSV row %d in %s", rowLine, sourcefile))
				continue
			}
			if idIndex >= 0 {
				if idIndex >= len(row) || strings.TrimSpace(row[idIndex]) == "" {
					warnings = append(warnings, fmt.Sprintf("Skipping service plan CSV row %d with empty id in %s", rowLine, sourcefile))
					continue
				}
			}
			if planIndex >= 0 {
				if planIndex >= len(row) || strings.TrimSpace(row[planIndex]) == "" {
					warnings = append(warnings, fmt.Sprintf("Skipping service plan CSV row %d with empty plan in %s", rowLine, sourcefile))
					continue
				}
			}
			obj := map[string]string{}
			for x, cell := range row {
				if x >= len(header) {
					continue
				}
				obj[header[x]] = cell
			}
			objs = append(objs, obj)
		}
		res = objs
	} else {
		res = []map[string]string{}
	}
	output, err := json.Marshal(res)
	if err != nil {
		return warnings, fmt.Errorf("marshal csv %s: %w", sourcefile, err)
	}
	fileout, err := os.OpenFile(destfile, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return warnings, fmt.Errorf("open json %s: %w", destfile, err)

	}
	defer fileout.Close()
	if err := fileout.Truncate(0); err != nil {
		return warnings, fmt.Errorf("truncate json %s: %w", destfile, err)
	}
	if _, err := fileout.Write(output); err != nil {
		return warnings, fmt.Errorf("write json %s: %w", destfile, err)
	}
	if _, err := fileout.Write([]byte("\n")); err != nil {
		return warnings, fmt.Errorf("write json newline %s: %w", destfile, err)
	}

	return warnings, nil
}

func isEmptyCSVRow(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
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
