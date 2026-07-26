package operatorapp

import (
	"testing"

	"github.com/paxlabs-inc/ion-agent/internal/provider"
)

func TestRuntimeSelectsMiMoGeneratorFromProviderOrModel(t *testing.T) {
	t.Parallel()
	for _, config := range []RuntimeConfig{
		{ProviderName: "Xiaomi", ProviderBaseURL: "https://example.invalid/v1", ProviderAPIKey: "test", ProviderModel: "model"},
		{ProviderName: "compatible", ProviderBaseURL: "https://example.invalid/v1", ProviderAPIKey: "test", ProviderModel: "MiMo-V2.5-Pro"},
	} {
		runtime := &Runtime{config: config}
		generator, _, _, err := runtime.generator()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := generator.(*provider.MiMoGenerator); !ok {
			t.Fatalf("provider %q model %q selected %T", config.ProviderName, config.ProviderModel, generator)
		}
		usage := providerUsageProjection(generator)
		if usage == nil {
			t.Fatal("MiMo generator did not expose capability status")
		}
		if _, ok := usage()["tool_capability"]; !ok {
			t.Fatalf("MiMo usage projection = %+v", usage())
		}
	}
}
