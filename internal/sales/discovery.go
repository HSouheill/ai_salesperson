package sales

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hussein/ai-salesperson/internal/sources"
)

// ErrSource means the prospecting source failed (network, quota, bad key).
var ErrSource = errors.New("prospecting source error")

type DiscoverInput struct {
	Source   string
	Industry string
	Location string
	OSMTag   string
	Limit    int
}

// Discover finds new prospects through an approved source and adds them to the
// agent. It only reads public directory data; nobody is contacted.
func (s *Service) Discover(ctx context.Context, orgID, agentID string, in DiscoverInput) (ImportResult, error) {
	var res ImportResult
	org, err := s.entitledOrg(ctx, orgID)
	if err != nil {
		return res, err
	}
	a, err := s.Store.GetAgent(ctx, orgID, agentID)
	if err != nil {
		return res, err
	}
	name := in.Source
	if name == "" {
		name = a.DiscoverySource
	}
	src, err := s.Sources.Get(org, name)
	if err != nil {
		return res, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if strings.TrimSpace(in.Industry) == "" && in.OSMTag == "" {
		return res, fmt.Errorf("%w: industry is required", ErrInvalid)
	}
	if strings.TrimSpace(in.Location) == "" {
		return res, fmt.Errorf("%w: location is required", ErrInvalid)
	}
	found, err := src.Discover(ctx, sources.Query{Industry: in.Industry, Location: in.Location, OSMTag: in.OSMTag, Limit: in.Limit})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return res, err
		}
		return res, fmt.Errorf("%w: %v", ErrSource, err)
	}
	return s.ImportProspects(ctx, orgID, agentID, src.Name(), found)
}
