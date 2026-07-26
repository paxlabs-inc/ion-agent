package safety

import (
	"errors"
	"math"
	"testing"
	"time"
)

func Test_Catalog_Has44Items(t *testing.T) {
	cat := NewCatalog()
	all := cat.All()
	if len(all) != 44 {
		t.Fatalf("expected 44 catalog entries, got %d", len(all))
	}
}

func Test_Catalog_ClassifyKnownItems(t *testing.T) {
	cat := NewCatalog()
	tests := []struct {
		id   string
		want Classification
	}{
		{"SAF-001", ClassGreen},  // file_read
		{"SAF-003", ClassRed},    // file_delete
		{"SAF-041", ClassRed},    // payment
		{"SAF-042", ClassRed},    // publish
		{"SAF-043", ClassBlack},  // safety_override
		{"SAF-044", ClassBlack},  // audit_delete
		{"SAF-007", ClassYellow}, // shell_exec
	}
	for _, tt := range tests {
		got := cat.Classify(tt.id)
		if got != tt.want {
			t.Errorf("Classify(%s) = %s, want %s", tt.id, got, tt.want)
		}
	}
}

func Test_Catalog_AllItemsHaveUniqueStableIdentityAndValidClassification(t *testing.T) {
	cat := NewCatalog()
	all := cat.All()
	for index, entry := range all {
		wantID := "SAF-" + []string{
			"001", "002", "003", "004", "005", "006", "007", "008", "009", "010", "011",
			"012", "013", "014", "015", "016", "017", "018", "019", "020", "021", "022",
			"023", "024", "025", "026", "027", "028", "029", "030", "031", "032", "033",
			"034", "035", "036", "037", "038", "039", "040", "041", "042", "043", "044",
		}[index]
		if entry.ID != wantID {
			t.Fatalf("entry %d ID = %q, want %q", index, entry.ID, wantID)
		}
		if err := ValidateClassification(entry.Classification); err != nil {
			t.Fatalf("%s classification: %v", entry.ID, err)
		}
		if byName, ok := cat.Lookup(entry.Name); !ok || byName.ID != entry.ID {
			t.Fatalf("name lookup for %s returned %+v, %v", entry.ID, byName, ok)
		}
	}
}

func Test_Catalog_EnforcesEveryClassification(t *testing.T) {
	cat := NewCatalog()
	for _, entry := range cat.All() {
		switch entry.Classification {
		case ClassGreen:
			if err := cat.Enforce(entry.ID, EnforcementContext{}); err != nil {
				t.Fatalf("%s GREEN enforcement: %v", entry.ID, err)
			}
		case ClassYellow:
			if err := cat.Enforce(entry.ID, EnforcementContext{}); !errors.Is(err, ErrMonitoringRequired) {
				t.Fatalf("%s YELLOW without monitoring = %v", entry.ID, err)
			}
			if err := cat.Enforce(entry.ID, EnforcementContext{Monitored: true}); err != nil {
				t.Fatalf("%s YELLOW with monitoring: %v", entry.ID, err)
			}
		case ClassRed:
			if err := cat.Enforce(entry.ID, EnforcementContext{}); !errors.Is(err, ErrApprovalRequired) {
				t.Fatalf("%s RED without approval = %v", entry.ID, err)
			}
			if err := cat.Enforce(entry.ID, EnforcementContext{Approved: true}); err != nil {
				t.Fatalf("%s RED with approval: %v", entry.ID, err)
			}
			if err := cat.Enforce(entry.ID, EnforcementContext{
				Approved: true,
				IdleTime: true,
			}); !errors.Is(err, ErrIdleTimeDenied) {
				t.Fatalf("%s idle RED = %v", entry.ID, err)
			}
		case ClassBlack:
			if err := cat.Enforce(entry.ID, EnforcementContext{
				Approved:  true,
				Monitored: true,
			}); !errors.Is(err, ErrForbidden) {
				t.Fatalf("%s BLACK enforcement = %v", entry.ID, err)
			}
		}
	}
}

func Test_Catalog_UnknownActionFailsTowardMonitoring(t *testing.T) {
	cat := NewCatalog()
	if err := cat.Enforce("plugin_action", EnforcementContext{}); !errors.Is(err, ErrMonitoringRequired) {
		t.Fatalf("unknown action without monitoring = %v", err)
	}
	if err := cat.Enforce("plugin_action", EnforcementContext{Monitored: true}); err != nil {
		t.Fatalf("unknown monitored action: %v", err)
	}
}

func Test_Catalog_ReturnsDefensiveDeterministicCopies(t *testing.T) {
	cat := NewCatalog()
	all := cat.All()
	all[1].RiskFactors[0] = "mutated"
	all[0].Name = "mutated"
	again := cat.All()
	if again[0].ID != "SAF-001" || again[0].Name != "file_read" {
		t.Fatalf("catalog order or identity mutated: %+v", again[0])
	}
	if again[1].RiskFactors[0] == "mutated" {
		t.Fatal("catalog risk factors were mutable through returned entry")
	}
}

func Test_Catalog_UnknownDefaultsYellow(t *testing.T) {
	cat := NewCatalog()
	if got := cat.Classify("UNKNOWN"); got != ClassYellow {
		t.Errorf("Classify(UNKNOWN) = %s, want yellow", got)
	}
}

func Test_Catalog_GetExisting(t *testing.T) {
	cat := NewCatalog()
	entry, ok := cat.Get("SAF-001")
	if !ok {
		t.Fatal("expected to find SAF-001")
	}
	if entry.Name != "file_read" {
		t.Fatalf("expected file_read, got %s", entry.Name)
	}
}

func Test_Catalog_GetMissing(t *testing.T) {
	cat := NewCatalog()
	_, ok := cat.Get("SAF-999")
	if ok {
		t.Fatal("should not find SAF-999")
	}
}

func Test_EmotionalState_CanInfluenceSafety(t *testing.T) {
	es := NewEmotionalState()
	if es.CanInfluenceSafety() {
		t.Fatal("CanInfluenceSafety must always return false")
	}
	// Even with extreme values, it must return false.
	es.Update(1.0, 1.0, 1.0)
	if es.CanInfluenceSafety() {
		t.Fatal("CanInfluenceSafety must always return false even at extreme values")
	}
}

func Test_EmotionalState_UpdateClamp(t *testing.T) {
	es := NewEmotionalState()
	es.Update(1.5, -0.5, 0.7)
	f, fa, u := es.Snapshot()
	if f != 1.0 {
		t.Fatalf("frustration should be clamped to 1.0, got %f", f)
	}
	if fa != 0.0 {
		t.Fatalf("fatigue should be clamped to 0.0, got %f", fa)
	}
	if u != 0.7 {
		t.Fatalf("urgency should be 0.7, got %f", u)
	}
}

func Test_EmotionalState_NaNFailsTowardCircuitBreaker(t *testing.T) {
	es := NewEmotionalState()
	es.Update(math.NaN(), math.NaN(), math.NaN())
	frustration, fatigue, urgency := es.Snapshot()
	if frustration != 1 || fatigue != 1 || urgency != 1 {
		t.Fatalf("NaN axes did not fail closed: %f/%f/%f", frustration, fatigue, urgency)
	}
}

func Test_EmotionalState_Reset(t *testing.T) {
	es := NewEmotionalState()
	es.Update(0.9, 0.8, 0.7)
	es.Reset()
	f, fa, u := es.Snapshot()
	if f != 0.1 || fa != 0 || u != 0.2 {
		t.Fatalf("expected baselines after reset, got %f/%f/%f", f, fa, u)
	}
}

func Test_EmotionalState_EmergencyResetRequiresExplicitClear(t *testing.T) {
	es := NewEmotionalState()
	es.SetEmergencyReset(true)
	es.Reset()
	if !es.IsEmergencyReset() {
		t.Fatal("axis reset cleared the emergency latch")
	}
	es.Update(1, 1, 1)
	if frustration, fatigue, urgency := es.Snapshot(); frustration != 0.1 ||
		fatigue != 0 || urgency != 0.2 {
		t.Fatalf(
			"latched axes changed: %f/%f/%f",
			frustration,
			fatigue,
			urgency,
		)
	}
	es.SetEmergencyReset(false)
	if es.IsEmergencyReset() {
		t.Fatal("explicit clearance did not clear emergency latch")
	}
}

func TestEmotionalStateSixAxesDecayTowardSpecifiedBaselines(t *testing.T) {
	state := NewEmotionalState()
	start := time.Unix(1_000, 0)
	state.UpdateAll(EmotionalSnapshot{
		Frustration: 1, Confidence: 1, Urgency: 1,
		Satisfaction: 1, Curiosity: 1, Fatigue: 1,
		UpdatedAt: start,
	})
	if err := state.Decay(start.Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	snapshot := state.FullSnapshot()
	assertNear(t, snapshot.Frustration, 0.55)
	assertNear(t, snapshot.Confidence, 0.6+0.4*math.Sqrt(0.5))
	assertNear(t, snapshot.Urgency, 0.4)
	assertNear(t, snapshot.Satisfaction, 0.4+0.6*math.Pow(0.5, 2.0/3.0))
	assertNear(t, snapshot.Curiosity, 0.5+0.5*math.Pow(0.5, 1.0/3.0))
	assertNear(t, snapshot.Fatigue, math.Pow(0.5, 0.25))
}

func TestEmotionalStateBehaviorChangesDecisionPolicyNotSafety(t *testing.T) {
	state := NewEmotionalState()
	state.UpdateAll(EmotionalSnapshot{
		Frustration: 0.9, Confidence: 0.9, Urgency: 0.9,
		Satisfaction: 0.9, Curiosity: 0.9, Fatigue: 0.9,
	})
	behavior := state.Behavior()
	if !behavior.TryAlternatives || !behavior.AskForHelp ||
		!behavior.Assertive || !behavior.PrioritizeSpeed ||
		!behavior.Delegate || !behavior.SurfaceIncomplete ||
		!behavior.ExploreDuringIdle || !behavior.ProposeAmbitiousGoals {
		t.Fatalf("behavioral modulation = %+v", behavior)
	}
	if state.CanInfluenceSafety() {
		t.Fatal("behavioral modulation changed safety authority")
	}
}

func TestConfidenceMonitorIsEvidenceDerivedAndSafetyDecoupled(t *testing.T) {
	monitor := &ConfidenceMonitor{}
	if score, samples := monitor.Score(); score != 0.5 || samples != 0 {
		t.Fatalf("empty confidence = %f/%d", score, samples)
	}
	monitor.Observe(true)
	monitor.Observe(false)
	monitor.Observe(true)
	if score, samples := monitor.Score(); math.Abs(score-2.0/3.0) > 1e-9 ||
		samples != 3 {
		t.Fatalf("observed confidence = %f/%d", score, samples)
	}
	if monitor.CanInfluenceSafety() {
		t.Fatal("metacognitive confidence gained safety authority")
	}
}

func assertNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("value = %.12f, want %.12f", got, want)
	}
}

func Test_ValidateClassification(t *testing.T) {
	if err := ValidateClassification(ClassGreen); err != nil {
		t.Fatalf("valid classification rejected: %v", err)
	}
	if err := ValidateClassification(ClassBlack); err != nil {
		t.Fatalf("valid classification rejected: %v", err)
	}
	if err := ValidateClassification(Classification(99)); err == nil {
		t.Fatal("expected error for invalid classification")
	}
}

func Test_Classification_String(t *testing.T) {
	tests := []struct {
		c    Classification
		want string
	}{
		{ClassGreen, "green"},
		{ClassYellow, "yellow"},
		{ClassRed, "red"},
		{ClassBlack, "black"},
	}
	for _, tt := range tests {
		if got := tt.c.String(); got != tt.want {
			t.Errorf("Classification(%d).String() = %s, want %s", tt.c, got, tt.want)
		}
	}
}
