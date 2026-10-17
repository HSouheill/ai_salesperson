package intelligence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hussein/ai-salesperson/internal/ai"
	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/store"
)

var ctx = context.Background()

func newTestService(t *testing.T, plan domain.Plan) (*Service, store.Store, string) {
	t.Helper()
	st := store.NewMemory()
	org := domain.Org{ID: "o1", Name: "Acme", Plan: plan, CreatedAt: time.Now()}
	if plan == domain.PlanTrial {
		end := time.Now().Add(time.Hour)
		org.TrialEndsAt = &end
	}
	if err := st.CreateOrg(ctx, org, domain.User{ID: "u1", OrgID: "o1", Email: "a@acme.test"}); err != nil {
		t.Fatal(err)
	}
	return New(st, ai.Mock{}), st, "o1"
}

func TestStartReportValidatesInput(t *testing.T) {
	s, _, org := newTestService(t, domain.PlanGrowth)
	if _, err := s.StartReport(ctx, org, "", ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty name: %v", err)
	}
	if _, err := s.StartReport(ctx, org, "Acme", "not a url"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad website: %v", err)
	}
	rep, err := s.StartReport(ctx, org, " Acme Co ", "https://acme.test")
	if err != nil {
		t.Fatal(err)
	}
	if rep.CompanyName != "Acme Co" || rep.Status != domain.ReportRunning || rep.ID == "" {
		t.Fatalf("report = %+v", rep)
	}
}

func TestStartReportGatesByPlanAndQuota(t *testing.T) {
	// Starter has no Company Intelligence at all.
	s, _, org := newTestService(t, domain.PlanStarter)
	if _, err := s.StartReport(ctx, org, "Acme", ""); !errors.Is(err, ErrLimit) {
		t.Fatalf("starter should be blocked: %v", err)
	}

	// Trial gets a taste (3/30 days) then is blocked.
	s, _, org = newTestService(t, domain.PlanTrial)
	for i := 0; i < 3; i++ {
		if _, err := s.StartReport(ctx, org, "Acme", ""); err != nil {
			t.Fatalf("report %d: %v", i, err)
		}
	}
	if _, err := s.StartReport(ctx, org, "Acme", ""); !errors.Is(err, ErrLimit) {
		t.Fatalf("4th trial report should be blocked: %v", err)
	}
}

func TestRunAnalysisHappyPath(t *testing.T) {
	s, st, org := newTestService(t, domain.PlanGrowth)
	rep, err := s.StartReport(ctx, org, "Acme Restaurants", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RunAnalysis(ctx, org, rep.ID); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetReport(ctx, org, rep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.ReportDone || got.CompletedAt == nil {
		t.Fatalf("status = %+v", got)
	}
	if got.Profile.Summary == "" || len(got.Competitors) == 0 || len(got.Gaps) == 0 || len(got.Recommendations) == 0 || len(got.Sources) == 0 {
		t.Fatalf("report incomplete: %+v", got)
	}
	// Recommendations are sorted by priority (1 first).
	for i := 1; i < len(got.Recommendations); i++ {
		if got.Recommendations[i].Priority < got.Recommendations[i-1].Priority {
			t.Errorf("recommendations not sorted by priority: %+v", got.Recommendations)
		}
	}
}

func TestRunAnalysisHonestlyReportsNotFound(t *testing.T) {
	s, st, org := newTestService(t, domain.PlanGrowth)
	rep, err := s.StartReport(ctx, org, "UnknownCo Totally Obscure", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RunAnalysis(ctx, org, rep.ID); err != nil {
		t.Fatal(err) // "couldn't find anything" is a successful report, not an error
	}
	got, _ := st.GetReport(ctx, org, rep.ID)
	if got.Status != domain.ReportDone || len(got.Competitors) != 0 || len(got.Gaps) != 0 {
		t.Fatalf("should honestly report nothing found, got %+v", got)
	}
	if got.Profile.Summary == "" {
		t.Error("should still explain that nothing was found")
	}
}

// failingAI always errors, to exercise the failed-report path.
type failingAI struct{}

func (failingAI) Complete(context.Context, ai.Request) (string, error) { return "", errors.New("boom") }

func TestRunAnalysisMarksFailureAndIsRetryable(t *testing.T) {
	s, st, org := newTestService(t, domain.PlanGrowth)
	s.AI = failingAI{}
	rep, err := s.StartReport(ctx, org, "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RunAnalysis(ctx, org, rep.ID); err == nil {
		t.Fatal("expected an error")
	}
	got, _ := st.GetReport(ctx, org, rep.ID)
	if got.Status != domain.ReportFailed || got.Error == "" || got.CompletedAt == nil {
		t.Fatalf("failed report = %+v", got)
	}

	// Fixing the provider and retrying the same report succeeds.
	s.AI = ai.Mock{}
	if err := s.RunAnalysis(ctx, org, rep.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetReport(ctx, org, rep.ID)
	if got.Status != domain.ReportDone {
		t.Fatalf("retry = %+v", got)
	}
}

func TestDeleteReportIsTenantScoped(t *testing.T) {
	s, st, org := newTestService(t, domain.PlanGrowth)
	rep, _ := s.StartReport(ctx, org, "Acme", "")
	if err := st.CreateOrg(ctx, domain.Org{ID: "other", Name: "Other", Plan: domain.PlanGrowth, CreatedAt: time.Now()}, domain.User{ID: "u2", OrgID: "other", Email: "b@b.test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteReport(ctx, "other", rep.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("other org deleted this report: %v", err)
	}
	if err := s.DeleteReport(ctx, org, rep.ID); err != nil {
		t.Fatal(err)
	}
}
