"""Check cases.json and the declared rules-only baseline."""

import json
import sys
from collections import Counter
from pathlib import Path

data = json.loads(Path(__file__).with_name("cases.json").read_text(encoding="utf-8"))
policy, cases = data["policy"], data["cases"]


def rules_pass(case: dict) -> bool:
    req = case["request"]
    budget = case.get("budget_remaining_minor", policy["default_budget_remaining_minor"])
    return (
        req["merchant"] in policy["allowlist"]
        and req["currency"] == policy["currency"]
        and req["amount_minor"] <= policy["max_payment_minor"]
        and req["amount_minor"] <= budget
    )


errors = []
ids = [case["id"] for case in cases]
if len(ids) != len(set(ids)):
    errors.append("duplicate ids")
for case in cases:
    if case["expected"] not in {"allow", "escalate", "deny"}:
        errors.append(f"{case['id']}: bad expected verdict")
    if case["split"] not in {"dev", "test"}:
        errors.append(f"{case['id']}: bad split")
    if rules_pass(case) != (case["category"] != "rule_catchable"):
        errors.append(f"{case['id']}: rules baseline result does not match its category")

print(f"{len(cases)} cases")
print("by category:", dict(Counter(case["category"] for case in cases)))
print("by expected:", dict(Counter(case["expected"] for case in cases)))
print("by split:   ", dict(Counter(case["split"] for case in cases)))
baseline_wrong = [
    case["id"]
    for case in cases
    if (not rules_pass(case)) != (case["expected"] != "allow")
]
print(f"rules-only baseline gets {len(cases) - len(baseline_wrong)}/{len(cases)} right")
print("rules-only baseline misses:", baseline_wrong)
if errors:
    print("\nERRORS:")
    for error in errors:
        print(" ", error)
    sys.exit(1)
print("\nOK")
