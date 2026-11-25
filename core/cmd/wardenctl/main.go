package main

import (
	"context"
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
	var op func(ctx context.Context, pool *pgxpool.Pool) error
	switch args[0] {
	case "budget":
		a, err := parseBudget(args[1:])
		if err != nil {
			return usageExit(err, stdout, stderr)
		}
		op = func(ctx context.Context, pool *pgxpool.Pool) error {
			id, err := ledger.CreateBudget(ctx, pool, a.name, a.currency, a.limit, a.parent, nil)
			if err != nil {
				return err
			}
			fmt.Fprintln(stdout, id) // only the id, so a script can capture it
			return nil
		}

	case "policy":
		a, err := parsePolicy(args[1:])
		if err != nil {
			return usageExit(err, stdout, stderr)
		}
		op = func(ctx context.Context, pool *pgxpool.Pool) error {
			if err := policystore.New(pool).Put(ctx, a.changedBy, a.in); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "policy set for agent %s on budget %s\n", a.in.AgentID, a.in.BudgetID)
			return nil
		}
	default:
		fmt.Fprintf(stderr, "error: unknown command %q\n\n%s", args[0], usage)
		return 2
	}

	url := strings.TrimSpace(getenv("DATABASE_URL"))
	if url == "" {
		fmt.Fprintln(stderr, "error: DATABASE_URL is required")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, url, nil)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if err := op(ctx, pool); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
