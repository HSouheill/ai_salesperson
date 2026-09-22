package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hussein/ai-salesperson/internal/domain"
)

const defaultPlaces = "https://places.googleapis.com/v1/places:searchText"

// Places discovers businesses through the Google Places API using the
// customer's own API key (their account, their quota, their terms).
type Places struct {
	url, key string
	enrich   Enricher
}

func (p *Places) Name() string { return "google_places" }

func (p *Places) Discover(ctx context.Context, q Query) ([]domain.Prospect, error) {
	if strings.TrimSpace(q.Industry) == "" || strings.TrimSpace(q.Location) == "" {
		return nil, fmt.Errorf("industry and location are required")
	}
	endpoint := p.url
	if endpoint == "" {
		endpoint = defaultPlaces
	}
	limit := clampLimit(q.Limit, 20, 60)
	var res []domain.Prospect
	token := ""
	for pages := 0; len(res) < limit && pages < 3; pages++ {
		body := map[string]any{"textQuery": q.Industry + " in " + q.Location, "pageSize": min(20, limit-len(res))}
		if token != "" {
			body["pageToken"] = token
		}
		b, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Goog-Api-Key", p.key)
		req.Header.Set("X-Goog-FieldMask", "places.id,places.displayName,places.formattedAddress,places.websiteUri,places.internationalPhoneNumber,places.rating,places.userRatingCount,places.businessStatus,nextPageToken")
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return nil, fmt.Errorf("Google Places request failed: %w", err)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			var e struct {
				Error struct{ Message string } `json:"error"`
			}
			_ = json.Unmarshal(raw, &e)
			return nil, fmt.Errorf("Google Places: %s", firstNonEmpty(e.Error.Message, fmt.Sprintf("status %d", resp.StatusCode)))
		}
		var out struct {
			Places []struct {
				DisplayName   struct{ Text string } `json:"displayName"`
				Address       string                `json:"formattedAddress"`
				Website       string                `json:"websiteUri"`
				Phone         string                `json:"internationalPhoneNumber"`
				Rating        float64               `json:"rating"`
				RatingCount   int                   `json:"userRatingCount"`
				BusinessState string                `json:"businessStatus"`
			} `json:"places"`
			Next string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("Google Places returned unusable data")
		}
		for _, pl := range out.Places {
			if pl.DisplayName.Text == "" || pl.BusinessState == "CLOSED_PERMANENTLY" || (pl.Website == "" && pl.Phone == "") {
				continue
			}
			notes := []string{"address: " + pl.Address}
			if pl.Rating > 0 {
				notes = append(notes, fmt.Sprintf("rated %.1f from %d reviews", pl.Rating, pl.RatingCount))
			}
			res = append(res, domain.Prospect{
				Name: pl.DisplayName.Text, Industry: q.Industry, Location: q.Location,
				Contact: domain.Contact{Website: pl.Website, Phone: pl.Phone},
				Notes:   strings.Join(append(notes, "listed on Google Maps"), "; "),
			})
		}
		if token = out.Next; token == "" {
			break
		}
	}
	if len(res) > limit {
		res = res[:limit]
	}
	if p.enrich != nil {
		for i := range res {
			p.enrich(ctx, &res[i])
		}
	}
	return res, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
