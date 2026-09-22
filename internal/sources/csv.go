package sources

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/hussein/ai-salesperson/internal/domain"
)

// ParseCSV reads a customer-provided list. A header row is required; recognised
// columns: name, contact_name, email, phone, website, industry, location, notes, deal_value.
func ParseCSV(r io.Reader) ([]domain.Prospect, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF")))] = i
	}
	if _, ok := idx["name"]; !ok {
		return nil, errors.New(`csv needs a "name" column`)
	}
	col := func(rec []string, k string) string {
		if i, ok := idx[k]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	var out []domain.Prospect
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if col(rec, "name") == "" {
			continue
		}
		var dv float64
		fmt.Sscanf(col(rec, "deal_value"), "%f", &dv)
		out = append(out, domain.Prospect{
			Name: col(rec, "name"), ContactName: col(rec, "contact_name"),
			Contact:  domain.Contact{Email: col(rec, "email"), Phone: col(rec, "phone"), Website: col(rec, "website")},
			Industry: col(rec, "industry"), Location: col(rec, "location"), Notes: col(rec, "notes"), DealValue: dv,
		})
	}
	return out, nil
}
