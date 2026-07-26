package privatecomputer

import (
	"errors"
	"testing"
)

func TestRailwayTopologyKeepsComputerPrivateAndPortable(t *testing.T) {
	topology := RailwayTopology()
	if err := topology.Validate(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*DeploymentTopology)
	}{
		{"computer public", func(candidate *DeploymentTopology) {
			candidate.Services[1].Public = true
		}},
		{"public control port", func(candidate *DeploymentTopology) {
			candidate.Services[1].PublicControlPort = true
		}},
		{"privileged", func(candidate *DeploymentTopology) {
			candidate.Services[1].Privileged = true
		}},
		{"root", func(candidate *DeploymentTopology) {
			candidate.Services[1].RunAsUID = 0
		}},
		{"ipv4 only", func(candidate *DeploymentTopology) {
			candidate.Services[1].BindAddress = "0.0.0.0"
		}},
		{"two volume writers", func(candidate *DeploymentTopology) {
			candidate.Services[0].Replicas = 2
		}},
		{"build-time migration", func(candidate *DeploymentTopology) {
			candidate.Volumes[0].StartTimeMigrations = false
		}},
		{"plain secret", func(candidate *DeploymentTopology) {
			candidate.Secrets[0].Sealed = false
		}},
		{"computer auth not shared", func(candidate *DeploymentTopology) {
			candidate.Secrets[0].Services = []string{"ion"}
		}},
		{"missing cost projection", func(candidate *DeploymentTopology) {
			candidate.Services[1].CostMicrosPerHour = 0
		}},
		{"stream past platform limit", func(candidate *DeploymentTopology) {
			candidate.Stream.MaximumConnection = candidate.Stream.PlatformLimit
		}},
		{"no continuous monitor", func(candidate *DeploymentTopology) {
			candidate.Services[0].ContinuousMonitoring = false
		}},
		{"railway-only export", func(candidate *DeploymentTopology) {
			candidate.PortableExport = ""
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := RailwayTopology()
			test.mutate(&candidate)
			if !errors.Is(candidate.Validate(), ErrInvalidContract) {
				t.Fatalf("unsafe topology accepted: %+v", candidate)
			}
		})
	}
}
