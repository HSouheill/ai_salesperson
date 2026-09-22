package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/hussein/ai-salesperson/internal/domain"
)

const defaultOverpass = "https://overpass-api.de/api/interpreter"

// osmTags maps common industry words to OpenStreetMap tags.
var osmTags = []struct {
	words []string
	tag   string
}{
	{[]string{"restaurant"}, "amenity=restaurant"},
	{[]string{"cafe", "coffee"}, "amenity=cafe"},
	{[]string{"bakery"}, "shop=bakery"},
	{[]string{"supermarket"}, "shop=supermarket"},
	{[]string{"grocery", "convenience"}, "shop=convenience"},
	{[]string{"hotel"}, "tourism=hotel"},
	{[]string{"pharmacy"}, "amenity=pharmacy"},
	{[]string{"clinic"}, "amenity=clinic"},
	{[]string{"dentist"}, "amenity=dentist"},
	{[]string{"gym", "fitness"}, "leisure=fitness_centre"},
	{[]string{"salon", "hairdresser", "barber"}, "shop=hairdresser"},
	{[]string{"real estate", "estate agent", "realtor"}, "office=estate_agent"},
	{[]string{"bank"}, "amenity=bank"},
	{[]string{"school"}, "amenity=school"},
	{[]string{"car repair", "garage", "mechanic"}, "shop=car_repair"},
	{[]string{"clothes", "clothing", "fashion"}, "shop=clothes"},
	{[]string{"electronics"}, "shop=electronics"},
	{[]string{"furniture"}, "shop=furniture"},
	{[]string{"lawyer", "law firm"}, "office=lawyer"},
	{[]string{"accountant", "accounting"}, "office=accountant"},
	{[]string{"advertising", "marketing agency"}, "office=advertising_agency"},
	{[]string{"software", "it company", "tech company"}, "office=it"},
	{[]string{"logistics", "freight", "courier"}, "office=logistics"},
	{[]string{"wholesale", "wholesaler"}, "shop=wholesale"},
}

var (
	reTag  = regexp.MustCompile(`^[a-z_:]+=[a-z0-9_:\-]+$`)
	reArea = regexp.MustCompile(`^[\p{L}\p{N} .,'’\-]{2,80}$`)
)

func osmTag(q Query) (string, error) {
	if q.OSMTag != "" {
		if !reTag.MatchString(q.OSMTag) {
			return "", fmt.Errorf("osm_tag must look like key=value")
		}
		return q.OSMTag, nil
	}
	ind := strings.ToLower(strings.TrimSpace(q.Industry))
	for _, t := range osmTags {
		for _, w := range t.words {
			if strings.Contains(ind, w) || (len(ind) > 3 && strings.Contains(w, strings.TrimSuffix(ind, "s"))) {
				return t.tag, nil
			}
		}
	}
	return "", fmt.Errorf("no OpenStreetMap category for industry %q; pass an explicit osm_tag such as amenity=restaurant", q.Industry)
}

// OSM discovers businesses from OpenStreetMap via the Overpass API. Only
// businesses that publish some contact detail (website, phone or email) are
// returned, since the rest cannot be contacted.
type OSM struct {
	url, contact string
	enrich       Enricher
}

func (o *OSM) Name() string { return "osm" }

func (o *OSM) Discover(ctx context.Context, q Query) ([]domain.Prospect, error) {
	tag, err := osmTag(q)
	if err != nil {
		return nil, err
	}
	if !reArea.MatchString(q.Location) {
		return nil, fmt.Errorf("location must be a place name such as \"Beirut\" or \"United Arab Emirates\"")
	}
	limit := clampLimit(q.Limit, 50, 200)
	k, v, _ := strings.Cut(tag, "=")
	query := fmt.Sprintf(`[out:json][timeout:25];area["name"=%q]->.a;nwr[%q=%q]["name"](area.a);out tags center %d;`,
		q.Location, k, v, limit*3) // over-fetch: many entries have no contact detail
	endpoint := o.url
	if endpoint == "" {
		endpoint = defaultOverpass
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(url.Values{"data": {query}}.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ua := "AISalesperson/1.0"
	if o.contact != "" {
		ua += " (" + o.contact + ")"
	}
	req.Header.Set("User-Agent", ua)
	resp, err := (&http.Client{Timeout: 45 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("OpenStreetMap lookup failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenStreetMap lookup failed: status %d (the public server may be busy; try again later)", resp.StatusCode)
	}
	var out struct {
		Elements []struct {
			Type string            `json:"type"`
			ID   int64             `json:"id"`
			Tags map[string]string `json:"tags"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("OpenStreetMap returned unusable data")
	}
	first := func(t map[string]string, keys ...string) string {
		for _, k := range keys {
			if s := strings.TrimSpace(t[k]); s != "" {
				return s
			}
		}
		return ""
	}
	seen := map[string]bool{}
	var res []domain.Prospect
	for _, e := range out.Elements {
		name := first(e.Tags, "name:en", "name")
		c := domain.Contact{
			Website: first(e.Tags, "website", "contact:website", "url"),
			Email:   first(e.Tags, "email", "contact:email"),
			Phone:   first(e.Tags, "phone", "contact:phone", "mobile", "contact:mobile"),
		}
		if name == "" || seen[strings.ToLower(name)] || (c.Website == "" && c.Email == "" && c.Phone == "") {
			continue
		}
		seen[strings.ToLower(name)] = true
		var facts []string
		for _, kv := range [][2]string{{"cuisine", "cuisine"}, {"brand", "brand"}, {"opening_hours", "opening hours"}, {"description", "description"}} {
			if s := e.Tags[kv[0]]; s != "" {
				facts = append(facts, kv[1]+": "+s)
			}
		}
		if a := strings.TrimSpace(e.Tags["addr:street"] + " " + e.Tags["addr:housenumber"]); a != "" {
			facts = append(facts, "address: "+a+" "+e.Tags["addr:city"])
		}
		p := domain.Prospect{
			Name: name, Contact: c, Industry: q.Industry,
			Location: first(e.Tags, "addr:city", "addr:town", "addr:village"),
			Notes:    strings.Join(append(facts, "listed in OpenStreetMap"), "; "),
		}
		if p.Location == "" {
			p.Location = q.Location
		}
		res = append(res, p)
		if len(res) >= limit {
			break
		}
	}
	if o.enrich != nil {
		for i := range res {
			o.enrich(ctx, &res[i])
		}
	}
	return res, nil
}
