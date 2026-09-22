package sources

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hussein/ai-salesperson/internal/domain"
)

func TestOSMDiscover(t *testing.T) {
	var query, ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		query, ua = string(b), r.Header.Get("User-Agent")
		w.Write([]byte(`{"elements":[
		 {"type":"node","id":1,"tags":{"name":"ABC Restaurant","website":"https://abc.test","phone":"+961 1 234","addr:city":"Beirut","cuisine":"lebanese"}},
		 {"type":"node","id":2,"tags":{"name":"No Contact Grill"}},
		 {"type":"way","id":3,"tags":{"name":"abc restaurant","email":"dup@abc.test"}},
		 {"type":"node","id":4,"tags":{"name":"Cedar Cafe","contact:email":"hi@cedar.test"}}]}`))
	}))
	defer srv.Close()

	f := Factory{OverpassURL: srv.URL, Contact: "ops@acme.test"}
	src, err := f.Get(domain.Org{}, "osm")
	if err != nil {
		t.Fatal(err)
	}
	got, err := src.Discover(context.Background(), Query{Industry: "Restaurants", Location: "Lebanon", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "ABC Restaurant" || got[1].Name != "Cedar Cafe" {
		t.Fatalf("results = %+v", got)
	}
	if got[0].Contact.Website != "https://abc.test" || got[0].Location != "Beirut" || !strings.Contains(got[0].Notes, "cuisine: lebanese") ||
		got[1].Contact.Email != "hi@cedar.test" {
		t.Errorf("mapping wrong: %+v", got)
	}
	if !strings.Contains(query, "amenity") || !strings.Contains(query, "restaurant") || !strings.Contains(query, "Lebanon") {
		t.Errorf("overpass query = %q", query)
	}
	if !strings.Contains(ua, "ops@acme.test") {
		t.Errorf("user agent should identify the operator, got %q", ua)
	}
}

func TestOSMRejectsInjectionAndUnknownIndustries(t *testing.T) {
	o := &OSM{url: "http://127.0.0.1:1"}
	for _, loc := range []string{`x"]; out; //`, "Beirut\n", "", `a"b`} {
		if _, err := o.Discover(context.Background(), Query{Industry: "restaurant", Location: loc}); err == nil ||
			!strings.Contains(err.Error(), "location") {
			t.Errorf("location %q must be rejected before any request, got %v", loc, err)
		}
	}
	if _, err := o.Discover(context.Background(), Query{Industry: "restaurant", Location: "Beirut", OSMTag: `amenity=x"]`}); err == nil {
		t.Error("malformed osm_tag must be rejected")
	}
	if _, err := o.Discover(context.Background(), Query{Industry: "space tourism", Location: "Beirut"}); err == nil ||
		!strings.Contains(err.Error(), "osm_tag") {
		t.Errorf("unknown industry should explain osm_tag, got %v", err)
	}
}

func TestPlacesDiscoverPaginatesAndFilters(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Goog-Api-Key") != "tenant-key" || !strings.Contains(r.Header.Get("X-Goog-FieldMask"), "places.websiteUri") {
			http.Error(w, `{"error":{"message":"bad key"}}`, 403)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if calls == 1 {
			if !strings.Contains(string(b), "supermarkets in Dubai") || strings.Contains(string(b), "pageToken") {
				t.Errorf("first request = %s", b)
			}
			w.Write([]byte(`{"places":[
			 {"displayName":{"text":"Fresh Mart"},"formattedAddress":"Al Wasl","websiteUri":"https://fresh.test","rating":4.5,"userRatingCount":120},
			 {"displayName":{"text":"Closed Mart"},"websiteUri":"https://closed.test","businessStatus":"CLOSED_PERMANENTLY"}],
			 "nextPageToken":"NEXT"}`))
			return
		}
		if !strings.Contains(string(b), `"pageToken":"NEXT"`) {
			t.Errorf("second request must carry the page token: %s", b)
		}
		w.Write([]byte(`{"places":[{"displayName":{"text":"Gulf Grocers"},"internationalPhoneNumber":"+971 4 000"}]}`))
	}))
	defer srv.Close()

	org := domain.Org{Settings: domain.OrgSettings{GooglePlacesKey: "tenant-key"}}
	src, err := Factory{PlacesURL: srv.URL}.Get(org, "google_places")
	if err != nil {
		t.Fatal(err)
	}
	got, err := src.Discover(context.Background(), Query{Industry: "supermarkets", Location: "Dubai", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Fresh Mart" || got[1].Name != "Gulf Grocers" || !strings.Contains(got[0].Notes, "rated 4.5 from 120 reviews") {
		t.Fatalf("results = %+v", got)
	}
	if _, err := (Factory{}).Get(domain.Org{}, "google_places"); err == nil {
		t.Error("google_places must require the customer's own key")
	}
	if _, err := (Factory{}).Get(domain.Org{}, "nope"); err == nil {
		t.Error("unknown source must be rejected")
	}
}

func TestParseCSV(t *testing.T) {
	csv := "\uFEFFName, Email ,deal_value,notes\nABC,a@abc.test,1500,four sites\n,skip@x.test,,\nSecond,,not-a-number,\n"
	got, err := ParseCSV(strings.NewReader(csv))
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %+v", err, got)
	}
	if got[0].Contact.Email != "a@abc.test" || got[0].DealValue != 1500 || got[0].Notes != "four sites" || got[1].DealValue != 0 {
		t.Errorf("rows = %+v", got)
	}
	if _, err := ParseCSV(strings.NewReader("email\nx@y.test\n")); err == nil {
		t.Error("a name column is required")
	}
}
