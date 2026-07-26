package privatecomputer

import (
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	RailwayPublicRequestLimit = 15 * time.Minute
	RailwayReconnectBefore    = 14 * time.Minute
)

type DeploymentTopology struct {
	Provider       string         `json:"provider"`
	Services       []Service      `json:"services"`
	Volumes        []Volume       `json:"volumes"`
	Secrets        []SecretRef    `json:"secrets"`
	Stream         StreamContract `json:"stream"`
	PortableExport string         `json:"portable_export"`
}

type Service struct {
	Name                 string   `json:"name"`
	Public               bool     `json:"public"`
	InternalHostname     string   `json:"internal_hostname"`
	BindAddress          string   `json:"bind_address"`
	Port                 uint16   `json:"port"`
	RunAsUID             int      `json:"run_as_uid"`
	Privileged           bool     `json:"privileged"`
	PublicControlPort    bool     `json:"public_control_port"`
	ReadOnlyRoot         bool     `json:"read_only_root"`
	DropCapabilities     []string `json:"drop_capabilities"`
	HealthPath           string   `json:"health_path"`
	ReadinessPath        string   `json:"readiness_path"`
	ContinuousMonitoring bool     `json:"continuous_monitoring"`
	StartTimeMigrations  bool     `json:"start_time_migrations"`
	RestartPolicy        string   `json:"restart_policy"`
	Replicas             int      `json:"replicas"`
	CPUVCores            float64  `json:"cpu_vcores"`
	MemoryBytes          int64    `json:"memory_bytes"`
	StorageBytes         int64    `json:"storage_bytes"`
	CostMicrosPerHour    int64    `json:"cost_micros_per_hour"`
	SleepPolicy          string   `json:"sleep_policy"`
}

type Volume struct {
	Name                string `json:"name"`
	Service             string `json:"service"`
	MountPath           string `json:"mount_path"`
	Optional            bool   `json:"optional"`
	SingleWriter        bool   `json:"single_writer"`
	NonRootOwnership    bool   `json:"non_root_ownership"`
	StartTimeMigrations bool   `json:"start_time_migrations"`
	BackupAndRestore    bool   `json:"backup_and_restore"`
	PortableFormat      string `json:"portable_format"`
	RedeployDowntime    bool   `json:"redeploy_downtime"`
}

type SecretRef struct {
	Name      string   `json:"name"`
	Generated bool     `json:"generated"`
	Sealed    bool     `json:"sealed"`
	Services  []string `json:"services"`
}

type StreamContract struct {
	PublicProxyService   string        `json:"public_proxy_service"`
	PrivateSourceService string        `json:"private_source_service"`
	Transport            string        `json:"transport"`
	MaximumFrameBytes    int           `json:"maximum_frame_bytes"`
	MaximumConnection    time.Duration `json:"maximum_connection"`
	PlatformLimit        time.Duration `json:"platform_limit"`
	ReplayCursor         bool          `json:"replay_cursor"`
	OriginBound          bool          `json:"origin_bound"`
	ExponentialBackoff   bool          `json:"exponential_backoff"`
}

func RailwayTopology() DeploymentTopology {
	return DeploymentTopology{
		Provider: "railway",
		Services: []Service{
			{
				Name: "ion", Public: true,
				InternalHostname: "ion.railway.internal",
				BindAddress:      "::", Port: 8080, RunAsUID: 10001,
				ReadOnlyRoot: true, DropCapabilities: []string{"ALL"},
				HealthPath: "/healthz", ReadinessPath: "/readyz",
				ContinuousMonitoring: true, StartTimeMigrations: true,
				RestartPolicy: "ON_FAILURE", Replicas: 1,
				CPUVCores: 1, MemoryBytes: 1 << 30, StorageBytes: 5 << 30,
				CostMicrosPerHour: 100_000, SleepPolicy: "disabled",
			},
			{
				Name: "ion-computer", Public: false,
				InternalHostname: "ion-computer.railway.internal",
				BindAddress:      "::", Port: 8081, RunAsUID: 10001,
				ReadOnlyRoot: true, DropCapabilities: []string{"ALL"},
				HealthPath: "/healthz", ReadinessPath: "/readyz",
				ContinuousMonitoring: true, StartTimeMigrations: true,
				RestartPolicy: "ON_FAILURE", Replicas: 1,
				CPUVCores: 2, MemoryBytes: 4 << 30, StorageBytes: 20 << 30,
				CostMicrosPerHour: 500_000,
				SleepPolicy:       "explicit_opt_in_with_reconciliation",
			},
		},
		Volumes: []Volume{
			{
				Name: "ion-data", Service: "ion", MountPath: "/var/lib/ion",
				SingleWriter: true, NonRootOwnership: true,
				StartTimeMigrations: true, BackupAndRestore: true,
				PortableFormat:   "encrypted-sqlite-and-artifact-archive",
				RedeployDowntime: true,
			},
			{
				Name: "ion-personal-computer", Service: "ion-computer",
				MountPath: "/var/lib/ion-computer/personal", Optional: true,
				SingleWriter: true, NonRootOwnership: true,
				StartTimeMigrations: true, BackupAndRestore: true,
				PortableFormat:   "posix-tar-with-encrypted-metadata",
				RedeployDowntime: true,
			},
		},
		Secrets: []SecretRef{
			{
				Name: "ION_COMPUTER_AUTH_KEY", Generated: true, Sealed: true,
				Services: []string{"ion", "ion-computer"},
			},
			{
				Name: "ION_SESSION_SIGNING_KEY", Generated: true, Sealed: true,
				Services: []string{"ion"},
			},
			{
				Name: "ION_PROVIDER_SECRET_REF", Sealed: true,
				Services: []string{"ion"},
			},
		},
		Stream: StreamContract{
			PublicProxyService: "ion", PrivateSourceService: "ion-computer",
			Transport: "websocket", MaximumFrameBytes: 1 << 20,
			MaximumConnection: RailwayReconnectBefore,
			PlatformLimit:     RailwayPublicRequestLimit,
			ReplayCursor:      true, OriginBound: true, ExponentialBackoff: true,
		},
		PortableExport: "oci-images-plus-encrypted-data-and-posix-workspace",
	}
}

func (topology DeploymentTopology) Validate() error {
	if topology.Provider != "railway" ||
		len(topology.Services) != 2 ||
		len(topology.Volumes) != 2 ||
		len(topology.Secrets) < 3 ||
		strings.TrimSpace(topology.PortableExport) == "" {
		return ErrInvalidContract
	}
	services := make(map[string]Service, len(topology.Services))
	publicCount := 0
	for _, service := range topology.Services {
		if err := service.validate(); err != nil {
			return err
		}
		if _, duplicate := services[service.Name]; duplicate {
			return ErrInvalidContract
		}
		services[service.Name] = service
		if service.Public {
			publicCount++
		}
	}
	ion, hasIon := services["ion"]
	computer, hasComputer := services["ion-computer"]
	if !hasIon || !hasComputer || publicCount != 1 || !ion.Public ||
		computer.Public || computer.PublicControlPort ||
		computer.InternalHostname != "ion-computer.railway.internal" {
		return ErrInvalidContract
	}
	volumeNames := make(map[string]struct{}, len(topology.Volumes))
	for _, volume := range topology.Volumes {
		if volume.validate(services) != nil {
			return ErrInvalidContract
		}
		if _, duplicate := volumeNames[volume.Name]; duplicate {
			return ErrInvalidContract
		}
		volumeNames[volume.Name] = struct{}{}
	}
	secretNames := make(map[string]struct{}, len(topology.Secrets))
	for _, secret := range topology.Secrets {
		if secret.validate(services) != nil {
			return ErrInvalidContract
		}
		if _, duplicate := secretNames[secret.Name]; duplicate {
			return ErrInvalidContract
		}
		secretNames[secret.Name] = struct{}{}
	}
	for _, required := range []string{
		"ION_COMPUTER_AUTH_KEY",
		"ION_SESSION_SIGNING_KEY",
		"ION_PROVIDER_SECRET_REF",
	} {
		if _, exists := secretNames[required]; !exists {
			return ErrInvalidContract
		}
	}
	computerAuth := secretByName(topology.Secrets, "ION_COMPUTER_AUTH_KEY")
	sessionSigning := secretByName(topology.Secrets, "ION_SESSION_SIGNING_KEY")
	providerSecret := secretByName(topology.Secrets, "ION_PROVIDER_SECRET_REF")
	if computerAuth == nil || !computerAuth.Generated ||
		!sameStrings(computerAuth.Services, []string{"ion", "ion-computer"}) ||
		sessionSigning == nil || !sessionSigning.Generated ||
		!sameStrings(sessionSigning.Services, []string{"ion"}) ||
		providerSecret == nil || providerSecret.Generated ||
		!sameStrings(providerSecret.Services, []string{"ion"}) {
		return ErrInvalidContract
	}
	if topology.Stream.validate(services) != nil {
		return ErrInvalidContract
	}
	return nil
}

func (service Service) validate() error {
	if service.Name == "" || service.InternalHostname == "" ||
		service.BindAddress != "::" || service.Port == 0 ||
		service.RunAsUID <= 0 || service.Privileged ||
		service.PublicControlPort || !service.ReadOnlyRoot ||
		!slices.Contains(service.DropCapabilities, "ALL") ||
		!validHealthPath(service.HealthPath) ||
		!validHealthPath(service.ReadinessPath) ||
		!service.ContinuousMonitoring || !service.StartTimeMigrations ||
		service.RestartPolicy != "ON_FAILURE" || service.Replicas != 1 ||
		service.CPUVCores <= 0 || service.MemoryBytes <= 0 ||
		service.StorageBytes <= 0 || service.CostMicrosPerHour <= 0 ||
		(service.SleepPolicy != "disabled" &&
			service.SleepPolicy != "explicit_opt_in_with_reconciliation") {
		return ErrInvalidContract
	}
	if service.InternalHostname != service.Name+".railway.internal" {
		return ErrInvalidContract
	}
	return nil
}

func (volume Volume) validate(services map[string]Service) error {
	service, exists := services[volume.Service]
	if !exists || strings.TrimSpace(volume.Name) == "" ||
		!filepath.IsAbs(volume.MountPath) ||
		!volume.SingleWriter || !volume.NonRootOwnership ||
		!volume.StartTimeMigrations || !volume.BackupAndRestore ||
		strings.TrimSpace(volume.PortableFormat) == "" ||
		!volume.RedeployDowntime || service.Replicas != 1 {
		return ErrInvalidContract
	}
	return nil
}

func (secret SecretRef) validate(services map[string]Service) error {
	if strings.TrimSpace(secret.Name) == "" || !secret.Sealed ||
		len(secret.Services) == 0 {
		return ErrInvalidContract
	}
	for _, service := range secret.Services {
		if _, exists := services[service]; !exists {
			return ErrInvalidContract
		}
	}
	return nil
}

func (stream StreamContract) validate(services map[string]Service) error {
	if stream.Transport != "websocket" ||
		stream.MaximumFrameBytes <= 0 ||
		stream.MaximumFrameBytes > 4<<20 ||
		stream.MaximumConnection <= 0 ||
		stream.PlatformLimit != RailwayPublicRequestLimit ||
		stream.MaximumConnection >= stream.PlatformLimit ||
		!stream.ReplayCursor || !stream.OriginBound ||
		!stream.ExponentialBackoff {
		return ErrInvalidContract
	}
	proxy, hasProxy := services[stream.PublicProxyService]
	source, hasSource := services[stream.PrivateSourceService]
	if !hasProxy || !hasSource || !proxy.Public || source.Public ||
		stream.PublicProxyService == stream.PrivateSourceService {
		return ErrInvalidContract
	}
	return nil
}

func validHealthPath(path string) bool {
	return strings.HasPrefix(path, "/") &&
		!strings.HasPrefix(path, "//") &&
		!strings.Contains(path, "..") &&
		!strings.ContainsAny(path, "?# \t\r\n") &&
		len(path) <= 128
}

func secretByName(secrets []SecretRef, name string) *SecretRef {
	for index := range secrets {
		if secrets[index].Name == name {
			return &secrets[index]
		}
	}
	return nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	slices.Sort(leftCopy)
	slices.Sort(rightCopy)
	return slices.Equal(leftCopy, rightCopy)
}
