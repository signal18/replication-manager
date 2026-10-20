package pbscommon

// The paper key markers and stripPaperKeyMarkers, taken unchanged from NimbusBackupClient's
// pbscommon/paperkey.go (left out: its QR codes need rsc.io/qr). The key file parser
// (crypt.go) uses them to accept a key pasted from a paper copy. See NOTICE.

import "strings"

const (
	paperKeyBegin = "-----BEGIN PROXMOX BACKUP KEY-----"
	paperKeyEnd   = "-----END PROXMOX BACKUP KEY-----"
)

// stripPaperKeyMarkers returns the key JSON of a paper key's text block
// (anything around the BEGIN/END marker lines, such as PBS's "Subject:" line,
// is dropped). Markers count only on a line of their own, so a hint quoting
// them stays intact. Plain JSON is returned unchanged.
func stripPaperKeyMarkers(data []byte) []byte {
	if strings.HasPrefix(strings.TrimSpace(string(data)), "{") {
		return data
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != paperKeyBegin {
			continue
		}
		body := lines[i+1:]
		for j, l := range body {
			if strings.TrimSpace(l) == paperKeyEnd {
				body = body[:j]
				break
			}
		}
		return []byte(strings.Join(body, "\n"))
	}
	return data
}
