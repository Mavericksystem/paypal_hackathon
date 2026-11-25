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

func usageExit(err error, stdout, stderr io.Writer) int {
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "error: %v\n\n%s", err, usage)
	return 2
}

type budgetArgs struct {
	name     string
	currency string
	limit    int64
	parent   string
}

func parseBudget(args []string) (budgetArgs, error) {
	fs := flag.NewFlagSet("budget", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "budget name")
	currency := fs.String("currency", "", "3 upper case letters")
	limit := fs.Int64("limit", 0, "limit in minor units")
	parent := fs.String("parent", "", "parent budget id (optional)")
	if err := fs.Parse(args); err != nil {
		return budgetArgs{}, err
	}
	if fs.NArg() > 0 {
		return budgetArgs{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if err := requireSet(fs, "name", "currency", "limit"); err != nil {
		return budgetArgs{}, err
	}
	if strings.TrimSpace(*name) == "" {
		return budgetArgs{}, errors.New("-name must not be blank")
	}
	if !validCurrency(*currency) {
		return budgetArgs{}, errors.New("-currency must be 3 upper case letters A to Z")
	}
	if *limit < 0 {
		return budgetArgs{}, errors.New("-limit must not be negative")
	}
	return budgetArgs{name: *name, currency: *currency, limit: *limit, parent: *parent}, nil
}
