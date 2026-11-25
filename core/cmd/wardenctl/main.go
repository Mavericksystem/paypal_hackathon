
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mavericksystem/warden/core/internal/db"
	"github.com/Mavericksystem/warden/core/internal/ledger"
	"github.com/Mavericksystem/warden/core/internal/policystore"
)

const usage = `usage:
  wardenctl budget -name NAME -currency USD -limit MINOR_UNITS [-parent BUDGET_ID]
  wardenctl policy -agent AGENT -budget BUDGET_ID -merchants m1,m2 -currency USD \
                   -max-per-payment N -auto-approve-max N -by WHO

DATABASE_URL must be set.

Every policy flag is required on purpose. For -max-per-payment and -auto-approve-max,
0 means "no limit", so it has to be written out. An empty -merchants "" denies every merchant.
`
func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
