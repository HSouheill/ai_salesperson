package httpapi_test

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/hussein/ai-salesperson/internal/auth"
)

func TestMain(m *testing.M) {
	auth.Cost = bcrypt.MinCost // hashing dominates test time otherwise
	os.Exit(m.Run())
}
