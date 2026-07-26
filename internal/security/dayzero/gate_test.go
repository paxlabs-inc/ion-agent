package dayzero

import (
	"context"
	"errors"
	"testing"
)

func TestGateNeverPassesMissingFailedOrEvidenceFreeChecks(t *testing.T) {
	t.Parallel()
	gate, err := New(map[ID]Check{
		"DZ-01": func(context.Context) (string, error) {
			return "go test ./internal/security/vault", nil
		},
		"DZ-02": func(context.Context) (string, error) {
			return " \t", nil
		},
		"DZ-03": func(context.Context) (string, error) {
			return "", errors.New("not implemented")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	report := gate.Evaluate(context.Background())
	if report.Passed() {
		t.Fatal("incomplete gate passed")
	}
	if len(report.Results) != 30 {
		t.Fatalf("results = %d, want 30", len(report.Results))
	}
	if report.Results[0].Status != Passed ||
		report.Results[1].Status != Failed ||
		report.Results[2].Status != Failed ||
		report.Results[3].Status != Missing {
		t.Fatalf("unexpected statuses: %+v", report.Results[:4])
	}
	if len(report.Unready()) != 29 {
		t.Fatalf("unready = %d, want 29", len(report.Unready()))
	}
}

func TestGateCancellationFailsEveryControlWithoutRunningChecks(t *testing.T) {
	t.Parallel()
	calls := 0
	gate, err := New(map[ID]Check{
		"DZ-01": func(context.Context) (string, error) {
			calls++
			return "must not run", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := gate.Evaluate(ctx)
	if report.Passed() || len(report.Results) != ControlCount || calls != 0 {
		t.Fatalf("report = %+v, calls = %d", report, calls)
	}
	for _, result := range report.Results {
		if result.Status != Failed || result.Error != context.Canceled.Error() {
			t.Fatalf("result = %+v", result)
		}
	}
}

func TestGatePassesOnlyAllThirtyEvidenceBackedChecks(t *testing.T) {
	t.Parallel()
	checks := make(map[ID]Check, ControlCount)
	for _, definition := range Definitions() {
		checks[definition.ID] = func(context.Context) (string, error) {
			return "automated-test-id", nil
		}
	}
	gate, err := New(checks)
	if err != nil {
		t.Fatal(err)
	}
	if report := gate.Evaluate(context.Background()); !report.Passed() {
		t.Fatalf("complete report did not pass: %+v", report.Unready())
	}
}

func TestReportCannotPassWithForgedOrEvidenceFreeResults(t *testing.T) {
	t.Parallel()
	results := make([]Result, 0, ControlCount)
	for _, definition := range Definitions() {
		results = append(results, Result{
			Definition: definition,
			Status:     Passed,
			Evidence:   "test evidence",
		})
	}
	report := Report{Results: results}
	if !report.Passed() {
		t.Fatal("canonical evidence-backed report did not pass")
	}
	report.Results[0].Evidence = " "
	if report.Passed() {
		t.Fatal("evidence-free report passed")
	}
	report.Results[0].Evidence = "restored"
	report.Results[0].Definition.ID = "DZ-30"
	if report.Passed() {
		t.Fatal("forged control order passed")
	}

	definitions := Definitions()
	definitions[0].ID = "forged"
	if Definitions()[0].ID != "DZ-01" {
		t.Fatal("Definitions returned mutable canonical storage")
	}
}

func TestGateRejectsUnknownAndNilChecks(t *testing.T) {
	t.Parallel()
	if _, err := New(map[ID]Check{"DZ-31": func(context.Context) (string, error) {
		return "x", nil
	}}); err == nil {
		t.Fatal("unknown check accepted")
	}
	if _, err := New(map[ID]Check{"DZ-01": nil}); err == nil {
		t.Fatal("nil check accepted")
	}
}
