// Package sources holds prospecting-source adapters. A source only returns
// prospects; it never contacts anyone. Only sources whose terms permit this
// use are included: OpenStreetMap (open data, no key) and Google Places
// (the customer's own API key), plus the customer's own lists.
package sources

import (
	"context"
	"fmt"
	"sort"

	"github.com/hussein/ai-salesperson/internal/domain"
)

type Query struct {
	Industry string
	Location string
	OSMTag   string // optional explicit OpenStreetMap tag, e.g. "amenity=restaurant"
	Limit    int
}

type Source interface {
	Name() string
	Discover(ctx context.Context, q Query) ([]domain.Prospect, error)
}

// Factory builds sources for an organization (Google Places needs the tenant's own key).
type Factory struct {
	OverpassURL string // default: public Overpass API
	PlacesURL   string // default: Google Places API
	Contact     string // contact info for the User-Agent, as OSM's usage policy asks
	Enrich      Enricher
}

// Enricher optionally fills in a public contact email from a business's own website.
type Enricher func(ctx context.Context, p *domain.Prospect)

func (f Factory) Get(org domain.Org, name string) (Source, error) {
	switch name {
	case "", "osm":
		return &OSM{url: f.OverpassURL, contact: f.Contact, enrich: f.Enrich}, nil
	case "google_places":
		if org.Settings.GooglePlacesKey == "" {
			return nil, fmt.Errorf("google_places needs your Google Places API key in Settings")
		}
		return &Places{url: f.PlacesURL, key: org.Settings.GooglePlacesKey, enrich: f.Enrich}, nil
	}
	return nil, fmt.Errorf("unknown source %q (available: %v)", name, Names())
}

func Names() []string {
	n := []string{"osm", "google_places"}
	sort.Strings(n)
	return n
}

func clampLimit(n, def, max int) int {
	if n <= 0 {
		return def
	}
	return min(n, max)
}
