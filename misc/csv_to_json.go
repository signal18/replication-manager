// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package misc

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

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
