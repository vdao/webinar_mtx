// Package csvutil parses participant lists from CSV files.
package csvutil

import (
	"encoding/csv"
	"io"
	"strings"

	"github.com/vdao/webinar_mtx/internal/webutil"
)

// ImportResult summarizes a parsed import file.
type ImportResult struct {
	Emails     []string // unique, normalized, valid emails ready to insert
	Names      []string // parallel to Emails (may be empty strings)
	Skipped    []string // invalid rows (truncated raw content)
	Duplicates int      // duplicate emails inside the file

	seen map[string]int // email -> index in Emails
}

const maxRows = 20000

// Parse reads a CSV with an optional header row and extracts email+name pairs.
// Understands columns named like email/e-mail/почта and name/имя/фио in ru/en,
// with either ',' or ';' delimiters. When no header is found, picks the first
// cell that looks like an email and uses the neighbouring non-email cell as name.
func Parse(r io.Reader) *ImportResult {
	res := &ImportResult{seen: map[string]int{}}
	data, err := io.ReadAll(io.LimitReader(r, 5<<20))
	if err != nil || len(data) == 0 {
		return res
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")

	records, rerr := readAll(text)
	if rerr != nil {
		records, _ = readAll(strings.ReplaceAll(text, ";", ","))
	}

	emailCol, nameCol := -1, -1
	if len(records) > 0 {
		for i, cell := range records[0] {
			c := strings.ToLower(strings.TrimSpace(cell))
			switch {
			case emailCol < 0 && (strings.Contains(c, "email") || strings.Contains(c, "почта") || strings.Contains(c, "имейл")):
				emailCol = i
			case nameCol < 0 && (c == "name" || c == "имя" || c == "фио" || c == "фамилия"):
				nameCol = i
			}
		}
		if emailCol >= 0 {
			records = records[1:] // consume the header row
		}
	}

	for n, rec := range records {
		if n >= maxRows {
			break
		}
		if len(rec) == 0 || allEmpty(rec) {
			continue
		}
		if emailCol >= 0 {
			res.consumeIndexed(rec, emailCol, nameCol)
		} else {
			res.consume(rec)
		}
	}
	return res
}

func readAll(text string) ([][]string, error) {
	cr := csv.NewReader(strings.NewReader(text))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	cr.Comma = detectDelimiter(text)
	return cr.ReadAll()
}

func (res *ImportResult) add(email, name string) {
	email = webutil.NormalizeEmail(email)
	name = strings.TrimSpace(name)
	if !webutil.ValidEmail(email) {
		res.Skipped = append(res.Skipped, truncate(email, 60))
		return
	}
	if idx, ok := res.seen[email]; ok {
		res.Duplicates++
		if res.Names[idx] == "" && name != "" {
			res.Names[idx] = name
		}
		return
	}
	res.seen[email] = len(res.Emails)
	res.Emails = append(res.Emails, email)
	res.Names = append(res.Names, name)
}

// consumeIndexed reads fixed columns.
func (res *ImportResult) consumeIndexed(rec []string, emailCol, nameCol int) {
	var email, name string
	if emailCol < len(rec) {
		email = rec[emailCol]
	}
	if nameCol >= 0 && nameCol < len(rec) {
		name = rec[nameCol]
	}
	res.add(email, name)
}

// consume scans for the first email-looking cell.
func (res *ImportResult) consume(rec []string) {
	emailIdx := -1
	for i, cell := range rec {
		cell = strings.TrimSpace(cell)
		if cell != "" && webutil.ValidEmail(cell) {
			emailIdx = i
			break
		}
	}
	if emailIdx < 0 {
		res.Skipped = append(res.Skipped, truncate(strings.Join(rec, " "), 60))
		return
	}
	name := ""
	for i, cell := range rec {
		if i != emailIdx && strings.TrimSpace(cell) != "" && !webutil.ValidEmail(cell) {
			name = strings.TrimSpace(cell)
			break
		}
	}
	res.add(rec[emailIdx], name)
}

func detectDelimiter(sample string) rune {
	if strings.Count(sample, ";") > strings.Count(sample, ",") {
		return ';'
	}
	return ','
}

func allEmpty(rec []string) bool {
	for _, c := range rec {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
