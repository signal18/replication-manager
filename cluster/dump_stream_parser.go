package cluster

import (
	"regexp"
	"strconv"
	"strings"
)

type dumpStreamParser struct {
	binlogRegex *regexp.Regexp
	gtidRegex   *regexp.Regexp
	wantBinlog  bool
	wantGTID    bool
	remaining   string
	onBinlog    func(file string, pos uint64)
	onGTID      func(gtid string)
}

func newDumpStreamParser(binlogRegex, gtidRegex *regexp.Regexp, parseBinlog, parseGTID bool, onBinlog func(string, uint64), onGTID func(string)) *dumpStreamParser {
	return &dumpStreamParser{
		binlogRegex: binlogRegex,
		gtidRegex:   gtidRegex,
		wantBinlog:  parseBinlog,
		wantGTID:    parseGTID,
		onBinlog:    onBinlog,
		onGTID:      onGTID,
	}
}

func (p *dumpStreamParser) Enabled() bool {
	return p.wantBinlog || p.wantGTID
}

func (p *dumpStreamParser) Consume(chunk []byte) {
	if !p.Enabled() || len(chunk) == 0 {
		return
	}
	content := p.remaining + string(chunk)
	lines := strings.Split(content, "\n")
	p.remaining = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		p.consumeLine(line)
		if !p.Enabled() {
			return
		}
	}
}

func (p *dumpStreamParser) Flush() {
	if !p.Enabled() || p.remaining == "" {
		p.remaining = ""
		return
	}
	p.consumeLine(p.remaining)
	p.remaining = ""
}

func (p *dumpStreamParser) consumeLine(line string) {
	if !p.Enabled() {
		return
	}
	line = strings.TrimRight(line, "\r")
	if p.wantBinlog && p.binlogRegex != nil {
		if matches := p.binlogRegex.FindStringSubmatch(line); len(matches) >= 3 {
			pos, _ := strconv.ParseUint(matches[2], 10, 64)
			if p.onBinlog != nil {
				p.onBinlog(matches[1], pos)
			}
			p.wantBinlog = false
		}
	}
	if p.wantGTID && p.gtidRegex != nil {
		if matches := p.gtidRegex.FindStringSubmatch(line); len(matches) >= 2 {
			gtid := firstNonEmpty(matches[1:])
			if gtid != "" {
				if p.onGTID != nil {
					p.onGTID(gtid)
				}
				p.wantGTID = false
			}
		}
	}
}

func firstNonEmpty(values []string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
