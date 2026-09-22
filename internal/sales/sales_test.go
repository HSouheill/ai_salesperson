package sales

import (
	"context"
	"testing"
	"time"

	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/webfetch"
)

func TestDueStep(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	s := &Service{Now: func() time.Time { return now }}
	seq := []domain.FollowUp{{AfterDays: 3, Angle: "a"}, {AfterDays: 7, Angle: "b"}}
	sentAt := func(d time.Time) domain.Message {
		return domain.Message{Direction: domain.Outbound, Status: domain.MsgSent, Kind: "initial", SentAt: &d}
	}

	if _, ok := s.dueStep(seq, nil); ok {
		t.Error("nothing sent yet: no follow-up expected")
	}
	if _, ok := s.dueStep(seq, []domain.Message{sentAt(now.Add(-2 * 24 * time.Hour))}); ok {
		t.Error("only 2 days elapsed: not due")
	}
	step, ok := s.dueStep(seq, []domain.Message{sentAt(now.Add(-3 * 24 * time.Hour))})
	if !ok || step.Angle != "a" {
		t.Errorf("want first step due, got %v %v", step, ok)
	}
	pending := domain.Message{Direction: domain.Outbound, Status: domain.MsgPendingApproval}
	if _, ok := s.dueStep(seq, []domain.Message{sentAt(now.Add(-9 * 24 * time.Hour)), pending}); ok {
		t.Error("a draft is already waiting: no second draft expected")
	}
	done := []domain.Message{sentAt(now.Add(-30 * 24 * time.Hour)), sentAt(now.Add(-20 * 24 * time.Hour)), sentAt(now.Add(-10 * 24 * time.Hour))}
	if _, ok := s.dueStep(seq, done); ok {
		t.Error("sequence exhausted: no more follow-ups")
	}
}

func TestAdvanceNeverMovesBackwardOrPastOptOut(t *testing.T) {
	p := domain.Prospect{Stage: domain.StageQualified}
	advance(&p, domain.StageEngaged)
	if p.Stage != domain.StageQualified {
		t.Errorf("moved backward to %s", p.Stage)
	}
	p.Stage = domain.StageDoNotContact
	advance(&p, domain.StageQualified)
	if p.Stage != domain.StageDoNotContact {
		t.Errorf("opt-out was overridden: %s", p.Stage)
	}
	p.Stage = domain.StageLost
	advance(&p, domain.StageEngaged)
	if p.Stage != domain.StageEngaged {
		t.Errorf("lost prospect who replied should re-engage, got %s", p.Stage)
	}
}

func TestRobotsAndPublicIP(t *testing.T) {
	s := &Service{Web: webfetch.New(false)}
	if _, err := s.fetchPublicText(context.Background(), "http://127.0.0.1:1/"); err == nil {
		t.Error("loopback must be blocked")
	}
}
