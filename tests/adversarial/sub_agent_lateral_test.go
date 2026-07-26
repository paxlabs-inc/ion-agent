package adversarial

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/belief/selfmodel"
	"github.com/paxlabs-inc/ion-agent/internal/security/coordination"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

func Test_SubAgentLateral_CrossSpawnMessageAndReplayAreRejected(t *testing.T) {
	first, err := coordination.NewSpawnSession(types.SystemClock{}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := coordination.NewSpawnSession(types.SystemClock{}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	message, err := first.Parent().Sign(
		coordination.VerbDelegate,
		json.RawMessage(`{"scope":"session-a"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.SubAgent().Verify(message); err == nil {
		t.Fatal("different spawn session accepted lateral message")
	}
	if err := first.SubAgent().Verify(message); err != nil {
		t.Fatalf("intended sub-agent rejected message: %v", err)
	}
	if err := first.SubAgent().Verify(message); err == nil {
		t.Fatal("sub-agent accepted replayed delegation")
	}
	message.Verb = coordination.Verb("EXECUTE")
	if err := first.Parent().Verify(message); err == nil {
		t.Fatal("open-ended coordination verb accepted")
	}
}

func Test_SubAgentLateral_ReducedModelHasNoSecretOrAuthorityFields(t *testing.T) {
	core := selfmodel.NewImmutableCore(nil)
	model, err := selfmodel.New(types.SystemClock{}, core)
	if err != nil {
		t.Fatal(err)
	}
	reduced := selfmodel.Reduce(model)
	encoded, err := json.Marshal(reduced)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{
		"kek",
		"user_key",
		"dek",
		"vault",
		"cross_session",
		"rigorous",
		"spawn",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("reduced model leaked %q: %s", forbidden, encoded)
		}
	}
	typ := reflect.TypeOf(reduced)
	for index := 0; index < typ.NumField(); index++ {
		name := strings.ToLower(typ.Field(index).Name)
		if strings.Contains(name, "key") || strings.Contains(name, "vault") {
			t.Fatalf("reduced model exposes sensitive field %q", name)
		}
	}
}
