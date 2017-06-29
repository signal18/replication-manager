package tools

import (
	"encoding/csv"
	"io"
	"strings"
)

// parses the raw stats CSV output to a map
func CsvToMap(csvInput string) (map[string]map[string]string, error) {

	csvReader := csv.NewReader(strings.NewReader(csvInput))
	lineCount := 0
	var headers []string

	m := make(map[string]map[string]string)

	for {
		// read just one record, but we could ReadAll() as well
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}

		// the first line are the headers, save them to a dedicated slice
		if lineCount == 0 {
			headers = record[:]
			lineCount += 1
		} else {
			n := make(map[string]string)
			for i := 0; i < len(headers); i++ {
				n[headers[i]] = record[i]
			}

			key := n["pxname"] + ":" + n["svname"]
			m[key] = n

			lineCount += 1
		}
	}
	return m, nil

}
