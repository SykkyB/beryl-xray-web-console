package vetlib

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"beryl-xray-web-console/internal/vless"
)

// ReadAndParse consumes vless:// lines from r and returns the entries
// that survived parsing plus a stats roll-up. The reader can be any
// stream — file contents, concatenated remote bodies, an in-memory
// buffer. Lines that don't start with "vless://" are silently skipped.
//
// Country is populated from the URL fragment's flag emoji here (cheap
// while we have the parsed URL in hand); empty if no flag was found.
func ReadAndParse(r io.Reader) ([]*Entry, *ParseStats, error) {
	st := &ParseStats{Reasons: map[string]int{}}
	var entries []*Entry
	sc := bufio.NewScanner(r)
	// Some public lists have 4KB+ single-line URLs with absurd query
	// strings; bump the scanner buffer to 1MB so we don't truncate.
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "vless://") {
			continue
		}
		st.Total++
		v, perr := vless.Parse(line)
		if perr != nil {
			st.Rejected++
			key := truncReason(perr.Error())
			st.Reasons[key]++
			continue
		}
		st.Parsed++
		entries = append(entries, &Entry{
			URL:       line,
			Name:      v.Name,
			Server:    v.Server,
			Port:      v.Port,
			SNI:       v.SNI,
			Transport: v.Type,
			Security:  v.Security,
			Country:   CountryFromName(v.Name),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("scan: %w", err)
	}
	return entries, st, nil
}

// DedupByAddr collapses entries that share the same Server:Port to one
// representative (the first seen). Returns the deduped slice and how
// many were dropped.
func DedupByAddr(in []*Entry) ([]*Entry, int) {
	seen := make(map[string]bool, len(in))
	out := make([]*Entry, 0, len(in))
	dropped := 0
	for _, e := range in {
		k := e.Server + ":" + itoa(e.Port)
		if seen[k] {
			dropped++
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	return out, dropped
}

func itoa(n int) string {
	// Avoid strconv import where this is the only use.
	if n == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		b[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

// truncReason trims a long parser error to its first sentence so the
// summary header doesn't carry 300-byte error messages.
func truncReason(s string) string {
	if i := strings.Index(s, "."); i >= 0 && i < 80 {
		return s[:i+1]
	}
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
