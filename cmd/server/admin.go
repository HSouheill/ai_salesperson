package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/hussein/ai-salesperson/internal/config"
	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/secrets"
	"github.com/hussein/ai-salesperson/internal/store"
)

const adminUsage = `usage:
  server set-plan <owner-email> <starter|growth|pro|enterprise>   assign a plan by hand (e.g. Enterprise for an agency)
Needs DATABASE_URL and SECRETS_KEY, the same as the server.`

// runAdmin handles operator commands and reports whether one was run.
// Enterprise is sold by contact, not through checkout, so this is how it is granted.
func runAdmin(args []string) bool {
	if len(args) == 0 || args[0] != "set-plan" {
		return false
	}
	if len(args) != 3 {
		fmt.Println(adminUsage)
		os.Exit(2)
	}
	email, plan := args[1], domain.Plan(args[2])
	switch plan {
	case domain.PlanStarter, domain.PlanGrowth, domain.PlanPro, domain.PlanEnterprise:
	default:
		fmt.Println(adminUsage)
		os.Exit(2)
	}
	cfg := config.Load()
	if cfg.DatabaseURL == "" || cfg.SecretsKey == "" {
		log.Fatal("set-plan needs DATABASE_URL and SECRETS_KEY")
	}
	box, err := secrets.New(cfg.SecretsKey)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	st, err := store.NewPostgres(ctx, cfg.DatabaseURL, box)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer st.Close()
	u, err := st.UserByEmail(ctx, email)
	if err != nil {
		log.Fatalf("no user with email %q", email)
	}
	org, err := st.GetOrg(ctx, u.OrgID)
	if err != nil {
		log.Fatal(err)
	}
	if org.ParentID != "" {
		log.Fatalf("%s belongs to an agency's client organization; change its plan from the agency's Clients page", email)
	}
	org.Plan, org.TrialEndsAt, org.Billing.Status = plan, nil, ""
	if err := st.UpdateOrg(ctx, org); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s (%s) is now on the %s plan\n", org.Name, org.ID, plan)
	return true
}
