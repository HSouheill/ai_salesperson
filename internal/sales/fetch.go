package sales

import (
	"context"
	"errors"
	"fmt"

	"github.com/hussein/ai-salesperson/internal/webfetch"
)

// fetchPublicText downloads a public web page and returns its visible text.
func (s *Service) fetchPublicText(ctx context.Context, raw string) (string, error) {
	h, err := s.Web.HTML(ctx, raw)
	switch {
	case errors.Is(err, webfetch.ErrInvalid), errors.Is(err, webfetch.ErrBlocked):
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	case err != nil:
		return "", fmt.Errorf("%w: fetch website: %v", ErrInvalid, err)
	}
	return webfetch.Text(h), nil
}
