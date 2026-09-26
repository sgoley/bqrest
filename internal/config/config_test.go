package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func validTestConfig() Config {
	return Config{
		Version: CurrentVersion,
		Connections: []Connection{{
			Name:               "analytics",
			ProjectID:          "example-project",
			Location:           "US",
			CredentialsFileEnv: "BQREST_CREDENTIALS_FILE",
			Datasets:           []string{"published_insights"},
			Resources: []Resource{{
				Dataset:        "published_insights",
				Table:          "monthly_revenue",
				EnabledColumns: []string{"month", "revenue"},
			}},
			Limits: QueryLimits{MaxBytesBilled: 1_000_000, MaxPageRows: 100, MaxResponseBytes: 1_000_000},
		}},
		Callers: []Caller{{
			Name:               "reporting-service",
			TokenEnv:           "BQREST_REPORTING_TOKEN",
			AllowedConnections: []string{"analytics"},
		}},
	}
}

func TestValidateStructureAcceptsValidConfig(t *testing.T) {
	if err := validTestConfig().ValidateStructure(); err != nil {
		t.Fatalf("ValidateStructure() error = %v", err)
	}
}

func TestValidateStructurePreservesFlexibleBigQueryColumnNames(t *testing.T) {
	cfg := validTestConfig()
	cfg.Connections[0].Resources[0].EnabledColumns = []string{"event date", "收入"}
	if err := cfg.ValidateStructure(); err != nil {
		t.Fatalf("ValidateStructure() rejected valid BigQuery column names: %v", err)
	}
}

func TestValidateStructureRejectsInvalidExposure(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name: "resource outside configured dataset",
			mutate: func(cfg *Config) {
				cfg.Connections[0].Resources[0].Dataset = "unconfigured"
			},
		},
		{
			name: "resource with no enabled columns",
			mutate: func(cfg *Config) {
				cfg.Connections[0].Resources[0].EnabledColumns = nil
			},
		},
		{
			name: "duplicate enabled column",
			mutate: func(cfg *Config) {
				cfg.Connections[0].Resources[0].EnabledColumns = []string{"revenue", "revenue"}
			},
		},
		{
			name: "caller granted an unknown connection",
			mutate: func(cfg *Config) {
				cfg.Callers[0].AllowedConnections = []string{"unknown"}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validTestConfig()
			test.mutate(&cfg)
			if err := cfg.ValidateStructure(); err == nil {
				t.Fatal("ValidateStructure() succeeded, want an error")
			}
		})
	}
}

func TestValidateChecksMountedCredentialAndToken(t *testing.T) {
	credentialsFile := filepath.Join(t.TempDir(), "service-account.json")
	if err := os.WriteFile(credentialsFile, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write test credential file: %v", err)
	}
	lookup := func(name string) (string, bool) {
		switch name {
		case "BQREST_CREDENTIALS_FILE":
			return credentialsFile, true
		case "BQREST_REPORTING_TOKEN":
			return "test-token", true
		default:
			return "", false
		}
	}
	if err := validTestConfig().Validate(lookup); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := validTestConfig().Validate(func(name string) (string, bool) {
		if name == "BQREST_CREDENTIALS_FILE" {
			return filepath.Join(t.TempDir(), "missing.json"), true
		}
		return "test-token", true
	}); err == nil {
		t.Fatal("Validate() accepted a missing credentials file")
	}
	if err := validTestConfig().Validate(func(name string) (string, bool) {
		if name == "BQREST_CREDENTIALS_FILE" {
			return credentialsFile, true
		}
		return "", false
	}); err == nil {
		t.Fatal("Validate() accepted a missing caller token")
	}
}

func TestWriteAtomicRetainsPermissionsAndConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("old contents"), 0o600); err != nil {
		t.Fatalf("write old config: %v", err)
	}

	want := validTestConfig()
	if err := WriteAtomic(path, want); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config permissions = %04o, want 0600", got)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read() after WriteAtomic(): %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("config after WriteAtomic() = %#v, want %#v", got, want)
	}
}

func TestWriteAtomicCreatesPrivateConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := WriteAtomic(path, validTestConfig()); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("new config permissions = %04o, want 0600", got)
	}
}
